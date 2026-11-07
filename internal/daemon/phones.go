package daemon

import (
	"context"
	"errors"
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

func pairingLifetime() time.Duration {
	lifetime, err := time.ParseDuration(os.Getenv(config.PairingLifetimeEnv))
	if err != nil || lifetime <= 0 {
		return cloudlink.PairingLifetime
	}
	return lifetime
}

type phonesRuntime struct {
	configuring sync.Mutex
	mu          sync.Mutex
	link        *cloudlink.Link
	stop        context.CancelFunc
	done        chan struct{}
	whyUnlinked string
}

const outpostUnlinked = "this daemon is an outpost; phones reach its agents through its home, so turn on phone access there"

func (d *Daemon) configurePhoneAccess() {
	d.phones.configuring.Lock()
	defer d.phones.configuring.Unlock()
	switch {
	case !d.phoneAccessEnabled():
		d.stopCloudLink("")
	case !d.enrollmentStatus().IsHome():
		d.stopCloudLink(outpostUnlinked)
	default:
		d.startCloudLink()
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
		d.phones.whyUnlinked = fmt.Sprintf("phone access is on, but this home's Attn Cloud keys could not be loaded: %v. Move cloud_sign_key and cloud_noise_key out of %s and turn phone access off and on to create new keys; every phone then pairs again", err, d.dataRoot)
		d.logf("cloudlink: %s", d.phones.whyUnlinked)
		return
	}
	d.phones.whyUnlinked = ""
	link := cloudlink.New(cloudlink.Config{CloudURL: cloudURL(), HomeName: homeDisplayName(), Identity: identity, Home: phoneHome{d}, PairingLifetime: pairingLifetime()})
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
	d.registerPhonePush()
}

func (d *Daemon) stopCloudLink(whyUnlinked string) {
	d.phones.mu.Lock()
	stop, done := d.phones.stop, d.phones.done
	d.phones.link, d.phones.stop, d.phones.done = nil, nil, nil
	d.phones.whyUnlinked = whyUnlinked
	d.phones.mu.Unlock()
	if stop == nil {
		return
	}
	d.unregisterPhonePush()
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
	err := d.ServeHomeGateway(HomeGatewayDevice{DeviceID: device.ID, ProfileID: device.ProfileID}, stream)
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
		Event:        protocol.EventPhonesUpdated,
		Link:         protocol.PhoneLink{State: protocol.PhoneLinkStateOff, CloudURL: cloudURL()},
		PairingCodes: []protocol.PhonePairingCode{},
		Pairings:     []protocol.PhonePairing{},
	}
	d.phones.mu.Lock()
	whyUnlinked := d.phones.whyUnlinked
	d.phones.mu.Unlock()
	if whyUnlinked != "" {
		message.Link.Error = protocol.Ptr(whyUnlinked)
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
		for _, pairingCode := range link.PairingCodes() {
			message.PairingCodes = append(message.PairingCodes, wirePairingCode(pairingCode))
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

func wirePairingCode(pairingCode cloudlink.PairingCode) protocol.PhonePairingCode {
	return protocol.PhonePairingCode{ProfileID: pairingCode.ProfileID, ExpiresAt: pairingCode.ExpiresAt.UTC().Format(time.RFC3339), Qr: pairingCode.QR}
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
	pairingCode, err := d.beginPhonePairing(client, msg.ProfileID)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.Success = true
		result.PairingCode = &pairingCode
	}
	d.sendToClient(client, result)
}

func (d *Daemon) beginPhonePairing(client *wsClient, profileID string) (protocol.PhonePairingCode, error) {
	link := d.cloudLink()
	if link == nil {
		d.phones.mu.Lock()
		whyUnlinked := d.phones.whyUnlinked
		d.phones.mu.Unlock()
		if whyUnlinked != "" {
			return protocol.PhonePairingCode{}, errors.New(whyUnlinked)
		}
		return protocol.PhonePairingCode{}, errors.New(phoneAccessOff)
	}
	profile, err := d.store.GetProfile(profileID)
	if err != nil || profile.Deleted() {
		return protocol.PhonePairingCode{}, fmt.Errorf("profile %q is not on this home", profileID)
	}
	pairingCode, err := link.NewPairingCode(profile.ID, profile.Name, client)
	if err != nil {
		return protocol.PhonePairingCode{}, err
	}
	select {
	case <-client.gone:
		link.CancelPairingCodesHeldBy(client)
	default:
	}
	return wirePairingCode(pairingCode), nil
}

func (d *Daemon) endPhonePairingCodesHeldBy(client *wsClient) {
	if link := d.cloudLink(); link != nil {
		link.CancelPairingCodesHeldBy(client)
	}
}

func (d *Daemon) handlePhonePairCancel(client *wsClient, msg *protocol.PhonePairCancelMessage) {
	if link := d.cloudLink(); link != nil {
		link.CancelPairingCode(msg.ProfileID)
	}
	d.sendToClient(client, protocol.PhonePairCancelResultMessage{Event: protocol.EventPhonePairCancelResult, RequestID: msg.RequestID, Success: true})
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
