package lib

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/qri-io/dataset"
	"github.com/qri-io/ioes"
	"github.com/qri-io/qri/auth/key"
	"github.com/qri-io/qri/base/params"
	"github.com/qri-io/qri/config"
	"github.com/qri-io/qri/dsref"
	dsrefspec "github.com/qri-io/qri/dsref/spec"
	"github.com/qri-io/qri/registry"
	"github.com/qri-io/qri/registry/regserver"
	"github.com/qri-io/qri/remote"
	repotest "github.com/qri-io/qri/repo/test"
)

func TestTwoActorRegistryIntegration(t *testing.T) {
	tr := NewNetworkIntegrationTestRunner(t, "integration_two_actor_registry")
	defer tr.Cleanup()

	nasim := tr.InitNasim(t)

	// - nasim creates a dataset
	ref := InitWorldBankDataset(tr.Ctx, t, nasim)

	// - nasim publishes to the registry
	PushToRegistry(tr.Ctx, t, nasim, ref.Alias())

	if err := AssertLogsEqual(nasim, tr.RegistryInst, ref); err != nil {
		t.Error(err)
	}

	refs, err := tr.RegistryInst.Collection().ListRawRefs(tr.Ctx, &EmptyParams{})
	if err != nil {
		t.Fatal(err)
	}
	t.Log(refs)

	hinshun := tr.InitHinshun(t)

	// - hinshun searches the registry for nasim's dataset name, gets a result
	if results := SearchFor(tr.Ctx, t, hinshun, "bank"); len(results) < 1 {
		t.Logf("expected at least one result in registry search")
	}

	// - hunshun fetches a preview of nasim's dataset
	// TODO (b5) - need to use the ref returned from search results
	t.Log(ref.String())
	Preview(tr.Ctx, t, hinshun, ref.String())

	// - hinshun pulls nasim's dataset
	Pull(tr.Ctx, t, hinshun, ref.Alias())

	if err := AssertLogsEqual(nasim, hinshun, ref); err != nil {
		t.Error(err)
	}

	// 5. nasim commits a new version
	ref = Commit2WorldBank(tr.Ctx, t, nasim)

	// 6. nasim re-publishes to the registry
	PushToRegistry(tr.Ctx, t, nasim, ref.Alias())

	// 7. hinshun logsyncs with the registry for world bank dataset, sees multiple versions
	_, err = hinshun.WithSource("network").Dataset().Pull(tr.Ctx, &PullParams{LogsOnly: true, Ref: ref.String()})
	if err != nil {
		t.Errorf("cloning logs: %s", err)
	}

	if err := AssertLogsEqual(nasim, hinshun, ref); err != nil {
		t.Error(err)
	}

	// TODO (b5) - assert hinshun DOES NOT have blocks for the latest commit to world bank dataset

	// 8. hinshun pulls latest version
	Pull(tr.Ctx, t, hinshun, ref.Alias())

	// TODO (b5) - assert hinshun has world bank dataset blocks

	// all three should now have the same HEAD reference & InitID
	dsrefspec.ConsistentResolvers(t, dsref.Ref{
		Username: ref.Username,
		Name:     ref.Name,
	},
		nasim.Repo(),
		hinshun.Repo(),
		tr.RegistryInst.Repo(),
	)
}

func TestReferencePulling(t *testing.T) {
	tr := NewNetworkIntegrationTestRunner(t, "integration_reference_pulling")
	defer tr.Cleanup()

	nasim := tr.InitNasim(t)

	// - nasim creates a dataset, publishes to registry
	ref := InitWorldBankDataset(tr.Ctx, t, nasim)
	PushToRegistry(tr.Ctx, t, nasim, ref.Alias())

	// - nasim's local repo should reflect publication
	logRes, err := nasim.Dataset().Activity(tr.Ctx, &ActivityParams{Ref: ref.Alias(), List: params.List{Limit: 1}})
	if err != nil {
		t.Fatal(err)
	}

	if logRes[0].Published != true {
		t.Errorf("nasim has published HEAD. ref[0] published is false")
	}

	hinshun := tr.InitHinshun(t)

	// fetch this from the registry by default
	p := &GetParams{Ref: "nasim/world_bank_population"}
	if _, err := hinshun.Dataset().Get(tr.Ctx, p); err != nil {
		t.Fatal(err)
	}

	// re-run. dataset should now be local, and no longer require registry to
	// resolve
	if _, err = hinshun.WithSource("local").Dataset().Get(tr.Ctx, p); err != nil {
		t.Fatal(err)
	}

	// create adnan
	adnan := tr.InitAdnan(t)

	// run a transform script that relies on world_bank_population, which adnan's
	// node should automatically pull to execute this script
	tfScriptData := `
wbp = load_dataset("nasim/world_bank_population")
ds = dataset.latest()

ds.body = wbp.body + [["g","h","i",False,3]]
dataset.commit(ds)
`
	scriptPath, err := tr.adnanRepo.WriteRootFile("transform.star", tfScriptData)
	if err != nil {
		t.Fatal(err)
	}

	saveParams := &SaveParams{
		Ref: "me/wbp_plus_one",
		FilePaths: []string{
			scriptPath,
		},
		Apply: true,
	}
	_, err = adnan.Dataset().Save(tr.Ctx, saveParams)
	if err != nil {
		t.Fatal(err)
	}

	// - adnan's local repo should reflect nasim's publication
	logRes, err = adnan.Dataset().Activity(tr.Ctx, &ActivityParams{Ref: ref.Alias(), List: params.List{Limit: 1}})
	if err != nil {
		t.Fatal(err)
	}

	if logRes[0].Published != true {
		t.Errorf("adnan's log expects head was published, ref[0] published is false")
	}
}

type NetworkIntegrationTestRunner struct {
	Ctx        context.Context
	prefix     string
	TestCrypto key.CryptoGenerator

	nasimRepo, hinshunRepo, adnanRepo *repotest.TempRepo
	Nasim, Hinshun, Adnan             *Instance

	registryRepo       *repotest.TempRepo
	Registry           registry.Registry
	RegistryInst       *Instance
	RegistryHTTPServer *httptest.Server
}

func NewNetworkIntegrationTestRunner(t *testing.T, prefix string) *NetworkIntegrationTestRunner {
	tr := &NetworkIntegrationTestRunner{
		Ctx:        context.Background(),
		prefix:     prefix,
		TestCrypto: repotest.NewTestCrypto(),
	}

	tr.InitRegistry(t)

	return tr
}

func (tr *NetworkIntegrationTestRunner) Cleanup() {
	if tr.RegistryHTTPServer != nil {
		tr.RegistryHTTPServer.Close()
	}
	if tr.registryRepo != nil {
		tr.registryRepo.Delete()
	}
	if tr.nasimRepo != nil {
		tr.nasimRepo.Delete()
	}
	if tr.hinshunRepo != nil {
		tr.hinshunRepo.Delete()
	}
}

func (tr *NetworkIntegrationTestRunner) InitNasim(t *testing.T) *Instance {
	r, err := repotest.NewTempRepo("nasim", fmt.Sprintf("%s_nasim", tr.prefix), tr.TestCrypto)
	if err != nil {
		t.Fatal(err)
	}

	if tr.RegistryHTTPServer != nil {
		cfg := r.GetConfig()
		cfg.Registry.Location = tr.RegistryHTTPServer.URL
		r.WriteConfigFile()
	}
	tr.nasimRepo = &r

	if tr.Nasim, err = NewInstance(tr.Ctx, r.QriPath, OptIOStreams(ioes.NewDiscardIOStreams())); err != nil {
		t.Fatal(err)
	}

	return tr.Nasim
}

func (tr *NetworkIntegrationTestRunner) InitHinshun(t *testing.T) *Instance {
	r, err := repotest.NewTempRepo("hinshun", fmt.Sprintf("%s_hinshun", tr.prefix), tr.TestCrypto)
	if err != nil {
		t.Fatal(err)
	}

	if tr.RegistryHTTPServer != nil {
		cfg := r.GetConfig()
		cfg.Registry.Location = tr.RegistryHTTPServer.URL
		r.WriteConfigFile()
	}
	tr.hinshunRepo = &r

	if tr.Hinshun, err = NewInstance(tr.Ctx, tr.hinshunRepo.QriPath, OptIOStreams(ioes.NewDiscardIOStreams())); err != nil {
		t.Fatal(err)
	}

	return tr.Hinshun
}

func (tr *NetworkIntegrationTestRunner) InitAdnan(t *testing.T) *Instance {
	r, err := repotest.NewTempRepo("adnan", fmt.Sprintf("%s_adnan", tr.prefix), tr.TestCrypto)
	if err != nil {
		t.Fatal(err)
	}

	if tr.RegistryHTTPServer != nil {
		cfg := r.GetConfig()
		cfg.Registry.Location = tr.RegistryHTTPServer.URL
		r.WriteConfigFile()
	}
	tr.adnanRepo = &r

	if tr.Adnan, err = NewInstance(tr.Ctx, r.QriPath, OptIOStreams(ioes.NewDiscardIOStreams())); err != nil {
		t.Fatal(err)
	}

	return tr.Adnan
}

func (tr *NetworkIntegrationTestRunner) InitRegistry(t *testing.T) {
	rr, err := repotest.NewTempRepo("registry", fmt.Sprintf("%s_registry", tr.prefix), tr.TestCrypto)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("registry qri path: %s", rr.QriPath)

	tr.registryRepo = &rr

	cfg := rr.GetConfig()
	cfg.Registry.Location = ""
	cfg.RemoteServer = &config.RemoteServer{
		Enabled:          true,
		AcceptSizeMax:    -1,
		AcceptTimeoutMs:  -1,
		RequireAllBlocks: false,
		AllowRemoves:     true,
	}

	rr.WriteConfigFile()

	tr.RegistryInst, err = NewInstance(tr.Ctx, rr.QriPath, OptIOStreams(ioes.NewDiscardIOStreams()))
	if err != nil {
		t.Fatal(err)
	}

	node := tr.RegistryInst.Node()
	if node == nil {
		t.Fatal("creating a Registry for NetworkIntegration test fails if `qri connect` is running")
	}

	rem, err := remote.NewServer(node, cfg.RemoteServer, node.Repo.Logbook(), tr.RegistryInst.Bus())
	if err != nil {
		t.Fatal(err)
	}

	tr.Registry = registry.Registry{
		Remote:   rem,
		Profiles: registry.NewMemProfiles(),
		Search:   regserver.MockRepoSearch{Repo: tr.RegistryInst.Repo()},
	}

	_, tr.RegistryHTTPServer = regserver.NewMockServerRegistry(tr.Registry)
}

func AssertLogsEqual(a, b *Instance, ref dsref.Ref) error {

	aLogs, err := a.logbook.DatasetRef(context.Background(), ref)
	if err != nil {
		return fmt.Errorf("fetching logs for a instance: %s", err)
	}

	bLogs, err := b.logbook.DatasetRef(context.Background(), ref)
	if err != nil {
		return fmt.Errorf("fetching logs for b instance: %s", err)
	}

	if aLogs.ID() != bLogs.ID() {
		return fmt.Errorf("log ID mismatch. %s != %s", aLogs.ID(), bLogs.ID())
	}

	if len(aLogs.Logs) != len(bLogs.Logs) {
		return fmt.Errorf("oplength mismatch. %d != %d", len(aLogs.Logs), len(bLogs.Logs))
	}

	return nil
}

func InitWorldBankDataset(ctx context.Context, t *testing.T, inst *Instance) dsref.Ref {
	res, err := inst.Dataset().Save(ctx, &SaveParams{
		Ref: "me/world_bank_population",
		Dataset: &dataset.Dataset{
			Meta: &dataset.Meta{
				Title: "World Bank Population",
			},
			BodyPath: "body.csv",
			BodyBytes: []byte(`a,b,c,true,2
d,e,f,false,3`),
			Readme: &dataset.Readme{
				ScriptPath: "readme.md",
				Text:       "#World Bank Population\nhow many people live on this planet?",
			},
		},
	})

	if err != nil {
		log.Fatalf("saving dataset version: %s", err)
	}

	return dsref.ConvertDatasetToVersionInfo(res).SimpleRef()
}

func Commit2WorldBank(ctx context.Context, t *testing.T, inst *Instance) dsref.Ref {
	res, err := inst.Dataset().Save(ctx, &SaveParams{
		Ref: "me/world_bank_population",
		Dataset: &dataset.Dataset{
			Meta: &dataset.Meta{
				Title: "World Bank Population",
			},
			BodyPath: "body.csv",
			BodyBytes: []byte(`a,b,c,true,2
d,e,f,false,3
g,g,i,true,4`),
		},
	})

	if err != nil {
		log.Fatalf("saving dataset version: %s", err)
	}

	return dsref.ConvertDatasetToVersionInfo(res).SimpleRef()
}

func PushToRegistry(ctx context.Context, t *testing.T, inst *Instance, refstr string) dsref.Ref {
	res, err := inst.WithSource("local").Dataset().Push(ctx, &PushParams{
		Ref: refstr,
	})

	if err != nil {
		t.Fatalf("publishing dataset: %s", err)
	}

	return *res
}

func SearchFor(ctx context.Context, t *testing.T, inst *Instance, term string) []registry.SearchResult {
	results, err := inst.Search().Search(ctx, &SearchParams{Query: term})
	if err != nil {
		t.Fatal(err)
	}

	return results
}

func Pull(ctx context.Context, t *testing.T, inst *Instance, refstr string) *dataset.Dataset {
	t.Helper()
	res, err := inst.WithSource("network").Dataset().Pull(ctx, &PullParams{Ref: refstr})
	if err != nil {
		t.Fatalf("cloning dataset %s: %s", refstr, err)
	}
	return res
}

func Preview(ctx context.Context, t *testing.T, inst *Instance, ref string) *dataset.Dataset {
	t.Helper()
	p := &PreviewParams{
		Ref: ref,
	}
	res, err := inst.Remote().Preview(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	return res
}
