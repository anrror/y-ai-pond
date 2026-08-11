package shadow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// OTACommand is the firmware update command published to model/update.
type OTACommand struct {
	DeviceID  string `json:"device_id"`
	Version   string `json:"version"`
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	ChunkSize int    `json:"chunk_size"`
	Chunks    int    `json:"chunks"`
}

// OTAManager handles chunked firmware transfer and checksum verification.
type OTAManager struct {
	chunkSize       int
	maxFirmwareSize int64
}

// NewOTAManager creates an OTAManager with the given chunk size (bytes) and
// maximum allowed firmware size (bytes).
func NewOTAManager(chunkSize int, maxSize int64) *OTAManager {
	return &OTAManager{
		chunkSize:       chunkSize,
		maxFirmwareSize: maxSize,
	}
}

// StageSlices splits firmware bytes into chunks of the configured chunk size.
// Returns an error if the firmware is empty or exceeds maxFirmwareSize.
func (m *OTAManager) StageSlices(fw []byte) ([][]byte, error) {
	if len(fw) == 0 {
		return nil, errors.New("shadow: firmware empty")
	}
	if int64(len(fw)) > m.maxFirmwareSize {
		return nil, fmt.Errorf("shadow: firmware size %d exceeds limit %d", len(fw), m.maxFirmwareSize)
	}

	chunks := (len(fw) + m.chunkSize - 1) / m.chunkSize
	slices := make([][]byte, 0, chunks)
	for i := 0; i < len(fw); i += m.chunkSize {
		end := i + m.chunkSize
		if end > len(fw) {
			end = len(fw)
		}
		slices = append(slices, fw[i:end])
	}
	return slices, nil
}

// BuildCommand assembles the OTACommand with SHA256 checksum and chunk metadata.
// It returns the command, the staged slices, or an error.
func (m *OTAManager) BuildCommand(deviceID, version, url string, fw []byte) (OTACommand, [][]byte, error) {
	slices, err := m.StageSlices(fw)
	if err != nil {
		return OTACommand{}, nil, err
	}

	h := sha256.Sum256(fw)
	cmd := OTACommand{
		DeviceID:  deviceID,
		Version:   version,
		URL:       url,
		SHA256:    hex.EncodeToString(h[:]),
		ChunkSize: m.chunkSize,
		Chunks:    len(slices),
	}
	return cmd, slices, nil
}

// VerifyChecksum checksum-verifies a received firmware blob against the
// expected SHA256 hex string.
func (m *OTAManager) VerifyChecksum(fw []byte, sha256hex string) bool {
	h := sha256.Sum256(fw)
	return hex.EncodeToString(h[:]) == sha256hex
}

// SendOTA builds an OTA command via the OTAManager, publishes it via MQTT,
// and retries up to 3 times on reporter error.
func (s *Service) SendOTA(ctx context.Context, deviceID, version, url string, fw []byte) error {
	cmd, _, err := s.ota.BuildCommand(deviceID, version, url, fw)
	if err != nil {
		return fmt.Errorf("shadow: SendOTA build: %w", err)
	}

	const maxRetries = 3
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		if err := s.reporter.PublishModelUpdate(ctx, deviceID, cmd); err != nil {
			lastErr = err
			s.log.Warn("shadow: OTA publish failed, retrying",
				"device", deviceID,
				"attempt", i+1,
				"error", err,
			)
			continue
		}
		return nil
	}
	return fmt.Errorf("shadow: SendOTA failed after %d retries: %w", maxRetries, lastErr)
}
