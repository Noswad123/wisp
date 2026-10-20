package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The compositor publishes this token atomically, in input callback order, not
// shell/process scheduling order. Go never writes the compositor's state file.
type hintSession struct {
	Token     string
	StatePath string
}

func parseHintSession(sub string, args []string) (*hintSession, error) {
	if sub != "show" && sub != "close" {
		return nil, errors.New("session options require hint show or close")
	}
	s := &hintSession{}
	for len(args) >= 2 {
		switch args[0] {
		case "--session":
			s.Token = args[1]
		case "--state":
			s.StatePath = args[1]
		default:
			return nil, fmt.Errorf("unknown hint option: %s", args[0])
		}
		args = args[2:]
	}
	if len(args) != 0 || s.Token == "" || strings.ContainsAny(s.Token, "\r\n") || (sub == "show" && !filepath.IsAbs(s.StatePath)) {
		return nil, errors.New("hint session requires --session TOKEN and show requires --state ABSOLUTE_PATH")
	}
	return s, nil
}

func (s *hintSession) token() string {
	if s == nil {
		return ""
	}
	return s.Token
}

func (s *hintSession) current() bool {
	if s == nil {
		return true
	}
	data, err := os.ReadFile(s.StatePath)
	return err == nil && string(data) == s.Token+"\n"
}

func showHintForSession(cfg envConfig, text string, session *hintSession) error {
	return withHintLock(cfg, func() error {
		// In particular, a show queued before Esc but scheduled after close must
		// not close a newer panel or launch an orphan from a canceled generation.
		if !session.current() {
			return nil
		}
		return showHintLockedForSession(cfg, text, session)
	})
}

func closeHintForSession(cfg envConfig, token string) error {
	return withHintLock(cfg, func() error {
		data, err := os.ReadFile(expand(cfg.hintPIDPath))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		var record hintProcessRecord
		if json.Unmarshal(data, &record) != nil || record.Session != token || token == "" {
			return nil
		}
		// Never remove or signal another generation, even if this close process
		// was spawned first. Standalone `hint close` remains unconditional.
		return closeHintLocked(cfg)
	})
}
