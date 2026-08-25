package tools

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// WuDecrypt wraps the wudecrypt binary
// (https://github.com/maki-chan/wudecrypt, AGPL-3.0) -- an external
// tool, not vendored into this repository (see daemon/pipelines/wiiu's
// package doc for why). It decrypts a raw Wii U disc dump given a
// platform-wide common key and a disc-specific key, both files
// supplied by the user; DiscEcho never embeds or fetches Wii U key
// material itself.
type WuDecrypt struct {
	Bin string
}

// NewWuDecrypt returns a WuDecrypt. Empty bin defaults to "wudecrypt".
func NewWuDecrypt(bin string) *WuDecrypt {
	if bin == "" {
		bin = "wudecrypt"
	}
	return &WuDecrypt{Bin: bin}
}

func (w *WuDecrypt) Name() string { return "wudecrypt" }

// Decrypt runs `wudecrypt <wudPath> <outDir> <commonKeyPath>
// <discKeyPath> GM`, extracting and decrypting only the GM (game)
// partition -- what a Cemu-style loader needs, skipping the
// update/channel partitions to keep the run fast. wudecrypt is
// community pre-alpha-quality code with no structured output; every
// line is forwarded to sink.Log and success is judged solely by exit
// code (the caller additionally checks that outDir actually gained
// content).
func (w *WuDecrypt) Decrypt(ctx context.Context, wudPath, outDir, commonKeyPath, discKeyPath string, sink Sink) error {
	cmd := exec.CommandContext(ctx, w.Bin, wudPath, outDir, commonKeyPath, discKeyPath, "GM") //nolint:gosec // args are daemon-configured paths, not user input; key file *contents* never appear on the command line or in logs.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("wudecrypt start: %w", err)
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if sink != nil && line != "" {
			sink.Log(state.LogLevelInfo, "wudecrypt: %s", line)
		}
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("wudecrypt: %w", err)
	}
	return nil
}
