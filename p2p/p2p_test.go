package p2p

import (
	"context"
	"testing"
	"time"

	"github.com/qri-io/dataset"
	"github.com/qri-io/qfs"
	"github.com/qri-io/qri/base"
	"github.com/qri-io/qri/base/dsfs"
	testcfg "github.com/qri-io/qri/config/test"
	"github.com/qri-io/qri/dsref"
	"github.com/qri-io/qri/event"
	p2ptest "github.com/qri-io/qri/p2p/test"
	"github.com/qri-io/qri/repo"
	reporef "github.com/qri-io/qri/repo/ref"
)

type testRunner struct {
	Ctx context.Context
}

func newTestRunner(t *testing.T) (tr *testRunner, cleanup func()) {
	tr = &testRunner{
		Ctx: context.Background(),
	}

	cleanup = func() {}
	return tr, cleanup
}

func (tr *testRunner) IPFSBackedQriNode(t *testing.T, username string) *QriNode {
	ipfs, _, err := p2ptest.MakeIPFSNode(tr.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, err := p2ptest.MakeRepoFromIPFSNode(tr.Ctx, ipfs, username, event.NilBus)
	if err != nil {
		t.Fatal(err)
	}
	localResolver := dsref.SequentialResolver(r.Dscache(), r)
	node, err := NewQriNode(r, testcfg.DefaultP2PForTesting(), event.NilBus, localResolver)
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func writeWorldBankPopulation(ctx context.Context, t *testing.T, r repo.Repo) reporef.DatasetRef {
	prevTs := dsfs.Timestamp
	dsfs.Timestamp = func() time.Time { return time.Time{} }
	defer func() { dsfs.Timestamp = prevTs }()

	ds := &dataset.Dataset{
		Name: "world_bank_population",
		Commit: &dataset.Commit{
			Title: "initial commit",
		},
		Meta: &dataset.Meta{
			Title: "World Bank Population",
		},
		Structure: &dataset.Structure{
			Format: "json",
			Schema: dataset.BaseSchemaArray,
		},
		Viz: &dataset.Viz{
			Format: "html",
		},
		Transform: &dataset.Transform{
			Syntax: "amaze",
		},
	}
	ds.SetBodyFile(qfs.NewMemfileBytes("body.json", []byte("[100]")))

	res, err := base.CreateDataset(ctx, r, r.Filesystem().DefaultWriteFS(), r.Profiles().Owner(ctx), ds, nil, base.SaveSwitches{Pin: true, ShouldRender: true})
	if err != nil {
		t.Fatal(err)
	}

	pro := r.Profiles().Owner(ctx)

	return reporef.DatasetRef{
		Peername:  pro.Peername,
		ProfileID: pro.ID,
		Name:      res.Name,
		Path:      res.Path,
	}
}
