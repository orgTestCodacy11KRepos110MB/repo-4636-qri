package dsref_test

import (
	"context"
	"testing"

	"github.com/qri-io/qri/dsref"
	dsrefspec "github.com/qri-io/qri/dsref/spec"
	"github.com/qri-io/qri/logbook/oplog"
	"github.com/qri-io/qri/profile"
)

func TestMemResolver(t *testing.T) {
	ctx := context.Background()
	m := dsref.NewMemResolver("test_peer_mem_resolver")

	if _, err := (*dsref.MemResolver)(nil).ResolveRef(ctx, nil); err != dsref.ErrRefNotFound {
		t.Errorf("ResolveRef must be nil-callable. expected: %q, got %v", dsref.ErrRefNotFound, err)
	}

	dsrefspec.AssertResolverSpec(t, m, func(ref dsref.Ref, author *profile.Profile, log *oplog.Log) error {
		m.Put(dsref.VersionInfo{
			InitID:    ref.InitID,
			ProfileID: author.ID.Encode(),
			Username:  ref.Username,
			Name:      ref.Name,
			Path:      ref.Path,
		})
		return nil
	})
}
