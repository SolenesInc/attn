package protocol

import (
	"encoding/binary"
	"fmt"
)

const BinaryFrameTypePtyOutput byte = 0x01

const BinaryFrameTypeKittyImage byte = 0x02

const binaryPtyHeaderBytes = 1 + 1 + 4

const binaryKittyImageHeaderBytes = 1 + 1 + 4 + 8 + 4 + 4 + 1

const (
	KittyImageFormatCodeRGB       byte = 0
	KittyImageFormatCodeRGBA      byte = 1
	KittyImageFormatCodeGrayAlpha byte = 2
	KittyImageFormatCodeGray      byte = 3
)

var kittyImageFormatNames = [...]string{
	KittyImageFormatCodeRGB:       "rgb",
	KittyImageFormatCodeRGBA:      "rgba",
	KittyImageFormatCodeGrayAlpha: "gray_alpha",
	KittyImageFormatCodeGray:      "gray",
}

func KittyImageFormatName(code byte) (string, bool) {
	if int(code) >= len(kittyImageFormatNames) {
		return "", false
	}
	return kittyImageFormatNames[code], true
}

func EncodeKittyImageFrame(sessionID string, imageID uint32, generation uint64, width, height uint32, format byte, pixels []byte) ([]byte, error) {
	if len(sessionID) == 0 || len(sessionID) > 255 {
		return nil, fmt.Errorf("session id length %d out of range [1,255]", len(sessionID))
	}
	if _, ok := KittyImageFormatName(format); !ok {
		return nil, fmt.Errorf("unknown kitty image format code %d", format)
	}
	if len(pixels) == 0 {
		return nil, fmt.Errorf("kitty image %d carries no pixels for its %dx%d header", imageID, width, height)
	}
	frame := make([]byte, binaryKittyImageHeaderBytes+len(sessionID)+len(pixels))
	frame[0] = BinaryFrameTypeKittyImage
	frame[1] = byte(len(sessionID))
	offset := 2 + copy(frame[2:], sessionID)
	binary.BigEndian.PutUint32(frame[offset:], imageID)
	binary.BigEndian.PutUint64(frame[offset+4:], generation)
	binary.BigEndian.PutUint32(frame[offset+12:], width)
	binary.BigEndian.PutUint32(frame[offset+16:], height)
	frame[offset+20] = format
	copy(frame[offset+21:], pixels)
	return frame, nil
}

type KittyImageFrame struct {
	SessionID  string
	ImageID    uint32
	Generation uint64
	Width      uint32
	Height     uint32
	Format     byte
	Pixels     []byte
}

func DecodeKittyImageFrame(frame []byte) (KittyImageFrame, error) {
	if len(frame) < binaryKittyImageHeaderBytes+1 {
		return KittyImageFrame{}, fmt.Errorf("frame too short: %d bytes", len(frame))
	}
	if frame[0] != BinaryFrameTypeKittyImage {
		return KittyImageFrame{}, fmt.Errorf("unknown binary frame type 0x%02x", frame[0])
	}
	idLen := int(frame[1])
	if idLen == 0 || len(frame) <= binaryKittyImageHeaderBytes+idLen {
		return KittyImageFrame{}, fmt.Errorf("frame too short for id length %d: %d bytes", idLen, len(frame))
	}
	offset := 2 + idLen
	format := frame[offset+20]
	if _, ok := KittyImageFormatName(format); !ok {
		return KittyImageFrame{}, fmt.Errorf("unknown kitty image format code %d", format)
	}
	return KittyImageFrame{
		SessionID:  string(frame[2:offset]),
		ImageID:    binary.BigEndian.Uint32(frame[offset:]),
		Generation: binary.BigEndian.Uint64(frame[offset+4:]),
		Width:      binary.BigEndian.Uint32(frame[offset+12:]),
		Height:     binary.BigEndian.Uint32(frame[offset+16:]),
		Format:     format,
		Pixels:     frame[binaryKittyImageHeaderBytes+idLen:],
	}, nil
}

func EncodePtyOutputFrame(sessionID string, seq uint32, data []byte) ([]byte, error) {
	if len(sessionID) == 0 || len(sessionID) > 255 {
		return nil, fmt.Errorf("session id length %d out of range [1,255]", len(sessionID))
	}
	frame := make([]byte, binaryPtyHeaderBytes+len(sessionID)+len(data))
	frame[0] = BinaryFrameTypePtyOutput
	frame[1] = byte(len(sessionID))
	offset := 2 + copy(frame[2:], sessionID)
	binary.BigEndian.PutUint32(frame[offset:], seq)
	copy(frame[offset+4:], data)
	return frame, nil
}

func DecodePtyOutputFrame(frame []byte) (sessionID string, seq uint32, data []byte, err error) {
	if len(frame) < binaryPtyHeaderBytes+1 {
		return "", 0, nil, fmt.Errorf("frame too short: %d bytes", len(frame))
	}
	if frame[0] != BinaryFrameTypePtyOutput {
		return "", 0, nil, fmt.Errorf("unknown binary frame type 0x%02x", frame[0])
	}
	idLen := int(frame[1])
	if idLen == 0 || len(frame) < binaryPtyHeaderBytes+idLen {
		return "", 0, nil, fmt.Errorf("frame too short for id length %d: %d bytes", idLen, len(frame))
	}
	sessionID = string(frame[2 : 2+idLen])
	seq = binary.BigEndian.Uint32(frame[2+idLen:])
	data = frame[binaryPtyHeaderBytes+idLen:]
	return sessionID, seq, data, nil
}
