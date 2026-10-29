// Package execution talks to the independent, host-privileged fixed-workload
// manager. It carries only bounded JSON over a configured Unix socket or a
// restricted SSH forced-command account; Docker credentials never reach AIM.
package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const (
	// MaxRequest and MaxResponse bound one transport exchange.
	MaxRequest  = 100000
	MaxResponse = 1048576
	// ExchangeTimeout is the manager's bounded single-request response budget.
	ExchangeTimeout = 8 * time.Second
)

var targetPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.@-]{0,252}$`)

// Rejected distinguishes a complete manager refusal from a transport failure.
// A refusal is still reconciled by stable identity before any replay decision.
type Rejected struct {
	Reason string
}

func (e *Rejected) Error() string { return "trusted execution manager refused: " + e.Reason }

// MaxRequestBytes and MaxResponseBytes let callers enforce the same finite
// envelope without duplicating protocol assumptions.
func MaxRequestBytes() int  { return MaxRequest }
func MaxResponseBytes() int { return MaxResponse }

// Exchange sends exactly one request and decodes exactly one bounded response.
// Exactly one of the socket or restricted SSH target must be configured.
func Exchange(ctx context.Context, request any, result any) error {
	payload, err := json.Marshal(request)
	if err != nil || len(payload)+1 > MaxRequest {
		return errors.New("execution request exceeds finite protocol budget")
	}
	payload = append(payload, '\n')
	ctx, cancel := context.WithTimeout(ctx, ExchangeTimeout)
	defer cancel()
	socketPath := os.Getenv("AIM_EXECUTION_SOCKET")
	target := os.Getenv("AIM_EXECUTION_SSH_TARGET")
	if (socketPath == "") == (target == "") {
		return errors.New("configure exactly one trusted execution transport")
	}
	var response []byte
	if socketPath != "" {
		if !strings.HasPrefix(socketPath, "/") {
			return errors.New("execution socket must be an absolute node-local path")
		}
		connection, err := (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		if err != nil {
			return errors.New("trusted execution socket unavailable")
		}
		defer connection.Close()
		stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
		defer stop()
		deadline, _ := ctx.Deadline()
		_ = connection.SetDeadline(deadline)
		if _, err := io.Copy(connection, bytes.NewReader(payload)); err != nil {
			return errors.New("execution request delivery unconfirmed")
		}
		response, err = io.ReadAll(io.LimitReader(connection, MaxResponse+1))
		if err != nil {
			return errors.New("execution response incomplete")
		}
	} else {
		if !targetPattern.MatchString(target) {
			return errors.New("invalid configured restricted execution SSH target")
		}
		args := []string{"-T", "-oBatchMode=yes", "-oStrictHostKeyChecking=yes", "-oConnectTimeout=5", "-oConnectionAttempts=1",
			"-oClearAllForwardings=yes", "-oRequestTTY=no", "-oPermitLocalCommand=no", "-oRemoteCommand=none"}
		if identityFile := os.Getenv("AIM_EXECUTION_SSH_IDENTITY_FILE"); identityFile != "" {
			if !strings.HasPrefix(identityFile, "/") {
				return errors.New("execution SSH identity path must be absolute")
			}
			args = append(args, "-oIdentitiesOnly=yes", "-i", identityFile)
		}
		args = append(args, "--", target)
		command := exec.CommandContext(ctx, "/usr/bin/ssh", args...)
		command.Stdin = bytes.NewReader(payload)
		stdout := &boundedBuffer{limit: MaxResponse}
		stderr := &boundedBuffer{limit: 4096}
		command.Stdout, command.Stderr = stdout, stderr
		command.WaitDelay = time.Second
		runErr := command.Run()
		response = stdout.Bytes()
		// The forced bridge exits 1 for a complete manager rejection response.
		// Parse that response, but never expose SSH stderr or remote diagnostics.
		if runErr != nil && (len(response) == 0 || ctx.Err() != nil) {
			return errors.New("restricted execution SSH response unconfirmed")
		}
	}
	if len(response) == 0 || len(response) > MaxResponse || response[len(response)-1] != '\n' {
		return errors.New("execution response exceeds budget or is incomplete")
	}
	var envelope struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if json.Unmarshal(response, &envelope) != nil {
		return errors.New("invalid trusted execution response")
	}
	if !envelope.OK {
		return &Rejected{Reason: envelope.Error}
	}
	if err := json.Unmarshal(envelope.Result, result); err != nil {
		return errors.New("invalid trusted execution result")
	}
	return nil
}

type boundedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	if len(data) > b.limit-b.buffer.Len() {
		return 0, errors.New("execution transport byte budget exceeded")
	}
	return b.buffer.Write(data)
}

func (b *boundedBuffer) Bytes() []byte { return b.buffer.Bytes() }
