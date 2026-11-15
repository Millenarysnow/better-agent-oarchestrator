package opencodev2

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	reportBridgePathEnv    = "AO_OPENCODE2_REPORT_BRIDGE"
	reportBridgeAddressEnv = "AO_OPENCODE2_REPORT_SOCKET"
	reportBridgeTimeout    = 10 * time.Second
	reportBridgeMaxRequest = 4 << 20
	reportBridgeMaxReply   = 16 << 10
)

var _ ports.AgentRuntimeLaunchEnv = (*Plugin)(nil)
var _ ports.AgentTerminalMessageSender = (*Plugin)(nil)

// AugmentRuntimeLaunchEnv binds the managed plugin's local bridge to this exact
// AO data directory and terminal generation. It does not launch another provider.
func (*Plugin) AugmentRuntimeLaunchEnv(env map[string]string, dataDir string, sessionID domain.SessionID, launchID string) {
	path, address := reportBridgeLocation(dataDir, sessionID, launchID)
	env[reportBridgePathEnv] = path
	env[reportBridgeAddressEnv] = address
}

type reportBridgeDescriptor struct {
	Version   int    `json:"version"`
	SessionID string `json:"sessionID"`
	LaunchID  string `json:"launchID"`
	Address   string `json:"address"`
	Token     string `json:"token"`
}

type reportBridgeRequest struct {
	Version         int    `json:"version"`
	Token           string `json:"token"`
	SessionID       string `json:"sessionID"`
	LaunchID        string `json:"launchID"`
	NativeSessionID string `json:"nativeSessionID"`
	DeliveryID      string `json:"deliveryID"`
	MessageID       string `json:"messageID"`
	Text            string `json:"text"`
}

type reportBridgeReply struct {
	Version         int    `json:"version"`
	SessionID       string `json:"sessionID"`
	LaunchID        string `json:"launchID"`
	NativeSessionID string `json:"nativeSessionID"`
	DeliveryID      string `json:"deliveryID"`
	MessageID       string `json:"messageID"`
	Accepted        bool   `json:"accepted"`
	Error           string `json:"error"`
}

func reportBridgeLocation(dataDir string, sessionID domain.SessionID, launchID string) (string, string) {
	scope := sha256.Sum256([]byte(filepath.Clean(dataDir) + "\x00" + string(sessionID) + "\x00" + launchID))
	key := hex.EncodeToString(scope[:16])
	return filepath.Join(dataDir, "opencode-v2", "terminal", key+".json"), reportBridgeAddress(dataDir, key)
}

func reportMessageID(nativeSessionID, deliveryID string) string {
	sum := sha256.Sum256([]byte(nativeSessionID + "\x00" + deliveryID))
	return "msg_" + hex.EncodeToString(sum[:])
}

// SendTerminalMessage returns only after the managed plugin's session.prompt
// call has returned a matching durable native inbox admission. Retrying uses the
// same native message ID even when a terminal or daemon restart changed LaunchID.
func (*Plugin) SendTerminalMessage(ctx context.Context, cfg ports.TerminalMessageConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	nativeID, err := terminalNativeID(cfg.NativeSessionID)
	if err != nil {
		return err
	}
	cfg.NativeSessionID = nativeID
	if cfg.DataDir == "" || cfg.SessionID == "" || cfg.LaunchID == "" || cfg.NativeSessionID == "" ||
		strings.TrimSpace(cfg.DeliveryID) == "" || strings.TrimSpace(cfg.Message) == "" {
		return errors.New("opencode-v2: terminal report requires a controller, native session and delivery identity")
	}
	path, address := reportBridgeLocation(cfg.DataDir, cfg.SessionID, cfg.LaunchID)
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opencode-v2: terminal report bridge unavailable; resume the terminal to load the managed plugin: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, reportBridgeMaxReply+1))
	_ = file.Close()
	if readErr != nil {
		return fmt.Errorf("opencode-v2: read terminal report bridge: %w", readErr)
	}
	var descriptor reportBridgeDescriptor
	if len(data) > reportBridgeMaxReply || json.Unmarshal(data, &descriptor) != nil {
		return errors.New("opencode-v2: invalid terminal report bridge descriptor")
	}
	token, tokenErr := hex.DecodeString(descriptor.Token)
	if descriptor.Version != 1 || descriptor.SessionID != string(cfg.SessionID) || descriptor.LaunchID != cfg.LaunchID ||
		descriptor.Address != address || tokenErr != nil || len(token) != 32 {
		return errors.New("opencode-v2: terminal report bridge does not match the current controller")
	}
	request := reportBridgeRequest{
		Version: 1, Token: descriptor.Token, SessionID: string(cfg.SessionID), LaunchID: cfg.LaunchID,
		NativeSessionID: cfg.NativeSessionID, DeliveryID: cfg.DeliveryID,
		MessageID: reportMessageID(cfg.NativeSessionID, cfg.DeliveryID), Text: cfg.Message,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("opencode-v2: encode terminal report: %w", err)
	}
	if len(payload)+1 > reportBridgeMaxRequest {
		return errors.New("opencode-v2: terminal report exceeds the local bridge frame limit")
	}
	ctx, cancel := context.WithTimeout(ctx, reportBridgeTimeout)
	defer cancel()
	conn, err := dialReportBridge(ctx, address)
	if err != nil {
		return fmt.Errorf("opencode-v2: connect terminal report bridge: %w", err)
	}
	defer func() { _ = conn.Close() }()
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCancel()
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("opencode-v2: submit terminal report: %w", err)
	}
	var reply reportBridgeReply
	if err := json.NewDecoder(io.LimitReader(conn, reportBridgeMaxReply)).Decode(&reply); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("opencode-v2: terminal report admission unconfirmed: %w", err)
	}
	if reply.Error != "" {
		return fmt.Errorf("opencode-v2: terminal report admission refused: %.500s", reply.Error)
	}
	if reply.Version != 1 || !reply.Accepted || reply.SessionID != request.SessionID || reply.LaunchID != request.LaunchID ||
		reply.NativeSessionID != request.NativeSessionID || reply.DeliveryID != request.DeliveryID || reply.MessageID != request.MessageID {
		return errors.New("opencode-v2: terminal report admission receipt does not match the delivery")
	}
	return nil
}
