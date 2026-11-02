package daemon

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/cloudlink"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

const defaultCloudURL = "https://cloud.attn.codes"

func cloudURL() string {
	if url := strings.TrimSpace(os.Getenv(config.CloudURLEnv)); url != "" {
		return strings.TrimRight(url, "/")
	}
	return defaultCloudURL
}

type phonesRuntime struct {
	mu   sync.Mutex
	link *cloudlink.Link
	stop context.CancelFunc
	done chan struct{}
}

func (d *Daemon) configurePhoneAccess() {
	if d.phoneAccessEnabled() {
		d.startCloudLink()
	} else {
		d.stopCloudLink()
	}
	d.publishFact(FactPhonesChanged, "", nil)
}

func (d *Daemon) startCloudLink() {
	d.phones.mu.Lock()
	defer d.phones.mu.Unlock()
	if d.phones.link != nil {
		return
	}
	identity, err := cloudlink.LoadIdentity(d.dataRoot)
	if err != nil {
		d.logf("cloudlink: loading the home's keys: %v", err)
		return
	}
	homeName, _ := os.Hostname()
	link := cloudlink.New(cloudlink.Config{CloudURL: cloudURL(), HomeName: homeName, Identity: identity, Home: phoneHome{d}})
	ctx, cancel := context.WithCancel(d.life.Context())
	done := make(chan struct{})
	if !d.life.Go("cloudLink", func() {
		defer close(done)
		link.Run(ctx)
	}) {
		cancel()
		return
	}
	d.phones.link, d.phones.stop, d.phones.done = link, cancel, done
}

func (d *Daemon) stopCloudLink() {
	d.phones.mu.Lock()
	stop, done := d.phones.stop, d.phones.done
	d.phones.link, d.phones.stop, d.phones.done = nil, nil, nil
	d.phones.mu.Unlock()
	if stop == nil {
		return
	}
	stop()
	<-done
}

func (d *Daemon) cloudLink() *cloudlink.Link {
	d.phones.mu.Lock()
	defer d.phones.mu.Unlock()
	return d.phones.link
}

type phoneHome struct{ d *Daemon }

func (h phoneHome) Pairings() ([]cloudlink.Pairing, error) {
	stored, err := h.d.store.PhonePairings()
	if err != nil {
		return nil, err
	}
	pairings := make([]cloudlink.Pairing, 0, len(stored))
	for _, p := range stored {
		pairings = append(pairings, cloudlink.Pairing{DeviceID: p.DeviceID, ProfileID: p.ProfileID, DeviceSignPub: p.DeviceSignPub, DeviceNoisePub: p.DeviceNoisePub, Name: p.Name})
	}
	return pairings, nil
}

func (h phoneHome) AddPairing(p cloudlink.Pairing) error {
	name := p.Name
	if name == "" {
		name = "Phone " + p.DeviceID[:6]
	}
	if err := h.d.store.AddPhonePairing(store.PhonePairing{DeviceID: p.DeviceID, ProfileID: p.ProfileID, DeviceSignPub: p.DeviceSignPub, DeviceNoisePub: p.DeviceNoisePub, Name: name, CreatedAt: time.Now()}); err != nil {
		return err
	}
	h.d.logf("phones: paired device %s (%s) with profile %s", p.DeviceID, name, p.ProfileID)
	return nil
}

func (h phoneHome) Serve(device cloudlink.Device, stream cloudlink.Stream) {
	d := h.d
	d.touchPhonePairing(device)
	err := d.ServeGateway(GatewayDevice{DeviceID: device.ID, ProfileID: device.ProfileID}, stream)
	d.touchPhonePairing(device)
	d.logf("phones: device %s left profile %s: %v", device.ID, device.ProfileID, err)
}

func (d *Daemon) touchPhonePairing(device cloudlink.Device) {
	if err := d.store.TouchPhonePairing(device.ID, device.ProfileID, time.Now()); err != nil {
		d.logf("phones: recording when device %s was last seen: %v", device.ID, err)
	}
	d.publishFact(FactPhonesChanged, device.ProfileID, nil)
}

func (h phoneHome) Changed() { h.d.publishFact(FactPhonesChanged, "", nil) }

func (h phoneHome) Logf(format string, args ...any) { h.d.logf(format, args...) }

func (d *Daemon) phonesSnapshot() protocol.PhonesUpdatedMessage {
	message := protocol.PhonesUpdatedMessage{
		Event:    protocol.EventPhonesUpdated,
		Link:     protocol.PhoneLink{State: protocol.PhoneLinkStateOff, CloudURL: cloudURL()},
		Offers:   []protocol.PhonePairingOffer{},
		Pairings: []protocol.PhonePairing{},
	}
	if link := d.cloudLink(); link != nil {
		status := link.Status()
		message.Link.State = protocol.PhoneLinkState(status.State)
		if status.Error != "" {
			message.Link.Error = protocol.Ptr(status.Error)
		}
		if status.HomeID != "" {
			message.Link.HomeID = protocol.Ptr(status.HomeID)
		}
		for _, offer := range link.Offers() {
			message.Offers = append(message.Offers, wireOffer(offer))
		}
	}
	pairings, err := d.store.PhonePairings()
	if err != nil {
		d.logf("phones snapshot: %v", err)
	}
	connected := map[cloudlink.Device]bool{}
	if link := d.cloudLink(); link != nil {
		connected = link.Connected()
	}
	for _, p := range pairings {
		message.Pairings = append(message.Pairings, protocol.PhonePairing{
			DeviceID: p.DeviceID, ProfileID: p.ProfileID, Name: p.Name,
			CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339), LastSeenAt: p.LastSeenAt.UTC().Format(time.RFC3339),
			Connected: connected[cloudlink.Device{ID: p.DeviceID, ProfileID: p.ProfileID}],
		})
	}
	return message
}

func wireOffer(offer cloudlink.Offer) protocol.PhonePairingOffer {
	return protocol.PhonePairingOffer{ProfileID: offer.ProfileID, ExpiresAt: offer.ExpiresAt.UTC().Format(time.RFC3339), Qr: offer.QR}
}

func (d *Daemon) projectPhones() {
	if d.wsHub == nil {
		return
	}
	d.projectSnapshot(snapshotPhones, func() {
		d.wsHub.BroadcastValue(d.phonesSnapshot())
	})
}

func (d *Daemon) handlePhonesGet(client *wsClient) {
	d.sendToClient(client, d.phonesSnapshot())
}

func (d *Daemon) handlePhonePairBegin(client *wsClient, msg *protocol.PhonePairBeginMessage) {
	result := protocol.PhonePairBeginResultMessage{Event: protocol.EventPhonePairBeginResult, RequestID: msg.RequestID}
	offer, err := d.beginPhonePairing(msg.ProfileID)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.Success = true
		result.Offer = &offer
	}
	d.sendToClient(client, result)
}

func (d *Daemon) beginPhonePairing(profileID string) (protocol.PhonePairingOffer, error) {
	link := d.cloudLink()
	if link == nil {
		return protocol.PhonePairingOffer{}, fmt.Errorf("%s", phoneAccessOff)
	}
	profile, err := d.store.GetProfile(profileID)
	if err != nil || profile.Deleted() {
		return protocol.PhonePairingOffer{}, fmt.Errorf("profile %q is not on this home", profileID)
	}
	offer, err := link.Offer(profile.ID, profile.Name)
	if err != nil {
		return protocol.PhonePairingOffer{}, err
	}
	return wireOffer(offer), nil
}

func (d *Daemon) handlePhonePairingRevoke(client *wsClient, msg *protocol.PhonePairingRevokeMessage) {
	result := protocol.PhonePairingRevokeResultMessage{Event: protocol.EventPhonePairingRevokeResult, RequestID: msg.RequestID}
	removed, err := d.store.RemovePhonePairing(msg.DeviceID, msg.ProfileID)
	switch {
	case err != nil:
		result.Error = protocol.Ptr(err.Error())
	case !removed:
		result.Error = protocol.Ptr(fmt.Sprintf("device %s has no pairing with profile %s", msg.DeviceID, msg.ProfileID))
	default:
		result.Success = true
		d.logf("phones: revoked device %s from profile %s", msg.DeviceID, msg.ProfileID)
		if link := d.cloudLink(); link != nil {
			link.Revoked(cloudlink.Device{ID: msg.DeviceID, ProfileID: msg.ProfileID})
		}
		d.publishFact(FactPhonesChanged, msg.ProfileID, nil)
	}
	d.sendToClient(client, result)
}
