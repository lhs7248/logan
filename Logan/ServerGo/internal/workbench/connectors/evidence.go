package connectors

import (
	"context"
	"encoding"
	"encoding/json"
	"math"
	"reflect"
)

// SourceProvenance records backend-validated immutable source/build association.
// Presence alone is not proof: the backend owns trusted mappings and event binding.
type SourceProvenance struct {
	IdentitySource string `json:"identitySource,omitempty"`
	MappingID      string `json:"mappingId"`
	RepoID         string `json:"repoId"`
	App            string `json:"app"`
	Platform       string `json:"platform"`
	Version        string `json:"version,omitempty"`
	Build          string `json:"build"`
	Commit         string `json:"commit"`
	Path           string `json:"path"`
	Start          int    `json:"start"`
	End            int    `json:"end"`
	ContentHash    string `json:"contentHash"`
	BlobHash       string `json:"blobHash"`
	EventID        string `json:"eventId,omitempty"`
	AssetID        string `json:"assetId,omitempty"`
}

type metadataBudget struct {
	bytes int64
	nodes int
}

func (b *metadataBudget) consume(n int64) error {
	b.bytes -= n
	b.nodes--
	if b.bytes < 0 || b.nodes < 0 {
		return ErrLimitExceeded
	}
	return nil
}

// copyMetadata accepts only bounded JSON-compatible values. It rejects custom
// marshalers, cycles/deep nesting and unsupported values before encoding allocates
// an unbounded buffer or executes caller-provided marshaling code.
func copyMetadata(ctx context.Context, v reflect.Value, depth int, b *metadataBudget) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if depth > 8 {
		return nil, ErrLimitExceeded
	}
	if err := b.consume(1); err != nil {
		return nil, err
	}
	if !v.IsValid() {
		return nil, nil
	}
	for v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil, nil
		}
		v = v.Elem()
	}
	if v.CanInterface() {
		if _, ok := v.Interface().(json.Marshaler); ok {
			return nil, ErrInvalidRequest
		}
		if _, ok := v.Interface().(encoding.TextMarshaler); ok {
			return nil, ErrInvalidRequest
		}
	}
	switch v.Kind() {
	case reflect.String:
		if err := b.consume(int64(len(v.String()))); err != nil {
			return nil, err
		}
		return v.String(), nil
	case reflect.Bool:
		return v.Bool(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if err := b.consume(20); err != nil {
			return nil, err
		}
		return v.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if err := b.consume(20); err != nil {
			return nil, err
		}
		return v.Uint(), nil
	case reflect.Float32, reflect.Float64:
		if math.IsInf(v.Float(), 0) || math.IsNaN(v.Float()) {
			return nil, ErrInvalidRequest
		}
		if err := b.consume(24); err != nil {
			return nil, err
		}
		return v.Float(), nil
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return nil, ErrInvalidRequest
		}
		if v.Len() > 128 {
			return nil, ErrLimitExceeded
		}
		result := map[string]any{}
		iter := v.MapRange()
		for iter.Next() {
			key := iter.Key().String()
			if err := b.consume(int64(len(key))); err != nil {
				return nil, err
			}
			value, err := copyMetadata(ctx, iter.Value(), depth+1, b)
			if err != nil {
				return nil, err
			}
			result[key] = value
		}
		return result, nil
	case reflect.Slice, reflect.Array:
		if v.Len() > 512 {
			return nil, ErrLimitExceeded
		}
		result := make([]any, v.Len())
		for i := 0; i < v.Len(); i++ {
			value, err := copyMetadata(ctx, v.Index(i), depth+1, b)
			if err != nil {
				return nil, err
			}
			result[i] = value
		}
		return result, nil
	default:
		return nil, ErrInvalidRequest
	}
}
func prepareEvidence(ctx context.Context, in []Evidence, consume func(string) bool, consumeSize func(int64) bool) ([]Evidence, error) {
	result := make([]Evidence, len(in))
	for i, item := range in {
		if item.Status == "" {
			item.Status = "unknown"
		}
		if item.Availability == "" {
			item.Availability = "unknown"
		}
		switch item.Status {
		case "matched", "candidate", "manual", "unknown":
		default:
			return nil, ErrInvalidRequest
		}
		for _, value := range []string{item.ID, item.Kind, item.Label, item.Content, item.Status, item.Availability} {
			if !consume(value) {
				return nil, ErrLimitExceeded
			}
		}
		if item.Kind == "manual" || item.Status == "manual" {
			item.Status = "manual"
			item.Verified = false
		}
		if item.Provenance != nil {
			proof := *item.Provenance
			item.Provenance = &proof
			for _, value := range []string{proof.IdentitySource, proof.MappingID, proof.RepoID, proof.App, proof.Platform, proof.Version, proof.Build, proof.Commit, proof.Path, proof.ContentHash, proof.BlobHash, proof.EventID, proof.AssetID} {
				if !consume(value) {
					return nil, ErrLimitExceeded
				}
			}
		}
		if item.Kind == "source" {
			proof := item.Provenance
			trusted := item.Verified && item.Status == "matched" && item.Availability == "available" && proof != nil
			if trusted {
				trusted = proof.MappingID != "" && proof.RepoID != "" && proof.App != "" && proof.Platform != "" && proof.Build != "" && commitPattern.MatchString(proof.Commit) && commitPattern.MatchString(proof.BlobHash) && proof.Path != "" && proof.Start > 0 && proof.End >= proof.Start && proof.ContentHash == digest([]byte(item.Content))
			}
			if !trusted {
				item.Verified = false
				if item.Status == "matched" {
					item.Status = "candidate"
				}
			}
		}
		if item.OriginRef != nil {
			budget := &metadataBudget{bytes: 16 << 10, nodes: 4096}
			copied, err := copyMetadata(ctx, reflect.ValueOf(item.OriginRef), 0, budget)
			if err != nil {
				return nil, err
			}
			item.OriginRef = copied.(map[string]any)
			if !consumeSize((16 << 10) - budget.bytes) {
				return nil, ErrLimitExceeded
			}
		}
		result[i] = item
	}
	return result, nil
}
