package main

import (
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"regexp"
)

var identityDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

type identityFlags struct {
	action, witness, digest, source, target string
	offline, writes, migration, fenced      bool
}

func (v identityFlags) validate() error {
	switch v.action {
	case "identity-export":
		if !v.offline || v.writes || v.migration || v.fenced || v.digest != "" || v.source != "" || v.target != "" || !privatePath(v.witness) {
			return errors.New("identity export flags")
		}
	case "identity-dry-run", "identity-migrate":
		if !v.offline || !privatePath(v.witness) || !identityDigest.MatchString(v.digest) || v.source == "" || v.target == "" || v.source == v.target {
			return errors.New("identity flags")
		}
		if v.action == "identity-migrate" {
			if !v.writes || !v.migration || !v.fenced {
				return errors.New("identity write guards")
			}
		} else if v.writes || v.migration || v.fenced {
			return errors.New("identity read only guards")
		}
	default:
		if v.witness != "" || v.digest != "" || v.source != "" || v.target != "" || v.migration || v.fenced {
			return errors.New("unexpected identity flags")
		}
	}
	return nil
}
func privatePath(p string) bool { return filepath.IsAbs(p) && filepath.Clean(p) == p }

// Refuse oversized evidence before publishing a file that the strict reader cannot reopen.
func encodeWitness(out io.Writer, witness any) error {
	body, err := json.Marshal(witness)
	if err != nil {
		return err
	}
	if len(body)+1 > maxPlanBytes {
		return errors.New("witness size")
	}
	body = append(body, '\n')
	_, err = out.Write(body)
	return err
}
