package main

import (
	"errors"
	"fmt"
	"github.com/juev/memcue/internal/jsonctx"
	"github.com/juev/memcue/internal/recall"
	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/vet"
	"io"
	"os"
	"strings"
	"syscall"
)

func (inv *invocation) runAddBatch(path string, force bool, out io.Writer) error {
	out = inv.writer(out)
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return errors.New("cannot open batch input")
	}
	f := os.NewFile(uintptr(fd), "batch input")
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return errors.New("batch input must be a regular file no larger than 64 MiB")
	}
	data, e := inv.readAll(io.LimitReader(f, (64<<20)+1))
	if e != nil {
		return e
	}
	var inputs []store.FactInput
	if e = jsonctx.Unmarshal(inv.ctx, data, &inputs, true); e != nil {
		return errors.New("invalid content-only batch JSON")
	}
	if inputs == nil {
		return errors.New("batch input must be a non-null JSON array")
	}
	for i := range inputs {
		in := &inputs[i]
		if strings.TrimSpace(in.Content) == "" {
			return errors.New("empty fact in batch")
		}
		source := in.Source
		if source == "" {
			source = "agent"
		}
		if !sources[source] || validKindScope(in.Kind, in.Scope) != nil || store.ValidExpiry(in.Expires) != nil {
			return errors.New("batch contains invalid source, kind, scope or expiry")
		}
		if !force && hasSecret(in.Content) {
			return errors.New("batch contains a possible secret; use a password manager or explicit -force")
		}
		if (source == "agent" || source == "tool") && !recall.IsShelf(in.Scope) {
			if vet.State(store.Fact{Content: in.Content, Meta: in.Meta}) != "" {
				return errors.New("batch contains installation state; " + strings.TrimRight(stateWaysOut, ".\n"))
			}
			if part := vet.CaptureNoise(store.Fact{Content: in.Content, Meta: in.Meta}); part != "" {
				return fmt.Errorf("batch contains %s, not reusable knowledge; use kind=event with an expiry or shelf:research as appropriate", part)
			}
		}
		in.Expires = captureExpires(in.Kind, source, in.Scope, in.Expires, false)
	}
	if len(inputs) == 0 {
		return nil
	} // An empty array is a validated no-op, without opening a backend.
	return inv.withStore(func(s *store.Store) error {
		facts, err := s.AddBatch(inputs)
		if err != nil {
			return err
		}
		inv.effect = true
		for _, f := range facts {
			if _, err := fmt.Fprintln(out, f.ID); err != nil {
				return mutationResponseError(f.ID, err)
			}
		}
		return nil
	})
}
