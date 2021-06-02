package lib

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/qri-io/dataset"
	"github.com/qri-io/dataset/preview"
	"github.com/qri-io/qri/automation/workflow"
	"github.com/qri-io/qri/base/dsfs"
	"github.com/qri-io/qri/dsref"
	"github.com/qri-io/qri/transform"
)

// AutomationMethods groups together methods for automations
type AutomationMethods struct {
	d dispatcher
}

// Name returns the name of this method group
func (m AutomationMethods) Name() string {
	return "automation"
}

// Attributes defines attributes for each method
func (m AutomationMethods) Attributes() map[string]AttributeSet {
	return map[string]AttributeSet{
		"apply":  {Endpoint: AEApply, HTTPVerb: "POST"},
		"deploy": {Endpoint: AEDeploy, HTTPVerb: "POST"},
	}
}

// ApplyParams are parameters for the apply command
type ApplyParams struct {
	Ref       string
	Transform *dataset.Transform
	Secrets   map[string]string
	Wait      bool
	// TODO(arqu): substitute with websockets when working over the wire
	ScriptOutput io.Writer `json:"-"`
}

// Validate returns an error if ApplyParams fields are in an invalid state
func (p *ApplyParams) Validate() error {
	if p.Ref == "" && p.Transform == nil {
		return fmt.Errorf("one or both of Reference, Transform are required")
	}
	return nil
}

// ApplyResult is the result of an apply command
type ApplyResult struct {
	Data  *dataset.Dataset
	RunID string `json:"runID"`
}

// Apply runs a transform script
func (m AutomationMethods) Apply(ctx context.Context, p *ApplyParams) (*ApplyResult, error) {
	got, _, err := m.d.Dispatch(ctx, dispatchMethodName(m, "apply"), p)
	if res, ok := got.(*ApplyResult); ok {
		return res, err
	}
	return nil, dispatchReturnError(got, err)
}

// type DeployParams = scheduler.DeployParams
// type DeployResponse = scheduler.DeployResponse

type DeployParams struct {
	Apply    bool
	Workflow *workflow.Workflow
	Ref      string
	Dataset  *dataset.Dataset
}

func (p *DeployParams) Validate() error {
	if p.Workflow == nil {
		return fmt.Errorf("deploy: workflow not set")
	}
	if p.Workflow.DatasetID == "" && p.Dataset == nil {
		return fmt.Errorf("deploy: either dataset or workflow.DatasetID are required")
	}
	return nil
}

type DeployResponse struct {
	Ref      string
	RunID    string
	Workflow *workflow.Workflow
}

func (m AutomationMethods) Deploy(ctx context.Context, p *DeployParams) (*DeployResponse, error) {
	got, _, err := m.d.Dispatch(ctx, dispatchMethodName(m, "deploy"), p)
	if res, ok := got.(*DeployResponse); ok {
		return res, err
	}
	return nil, dispatchReturnError(got, err)
}

// Implementations for automation methods follow

// automationImpl holds the method implementations for automations
type automationImpl struct{}

// Apply runs a transform script
func (automationImpl) Apply(scope scope, p *ApplyParams) (*ApplyResult, error) {
	ctx := scope.Context()
	log.Debugw("applying transform", "ref", p.Ref, "wait", p.Wait)

	var err error
	ref := dsref.Ref{}
	if p.Ref != "" {
		scope.EnableWorkingDir(true)
		ref, _, err = scope.ParseAndResolveRef(ctx, p.Ref)
		if err != nil {
			return nil, err
		}
	}

	ds := &dataset.Dataset{}
	if !ref.IsEmpty() {
		ds.Name = ref.Name
		ds.Peername = ref.Username
	}
	if p.Transform != nil {
		ds.Transform = p.Transform
		ds.Transform.OpenScriptFile(ctx, scope.Filesystem())
	}

	wf := &workflow.Workflow{}

	runID, err := scope.Automation().ApplyWorkflow(ctx, p.Wait, p.ScriptOutput, wf, ds, p.Secrets)
	if err != nil {
		return nil, err
	}

	res := &ApplyResult{}
	if p.Wait {
		ds, err := preview.Create(ctx, ds)
		if err != nil {
			return nil, err
		}
		res.Data = ds
	}
	res.RunID = runID
	return res, nil
}

func (automationImpl) Deploy(scope scope, p *DeployParams) (*DeployResponse, error) {
	log.Debugw("deploying dataset", "datasetID", p.Workflow.DatasetID)

	var (
		runID string
		err   error
	)

	// TODO(b5): calling apply *first* allows us to supply datasets that only have
	// a transform function. This should also let us remove apply from the save
	// path
	if p.Apply {
		runID, err = scope.Automation().ApplyWorkflow(scope.ctx, true, os.Stdout, p.Workflow, p.Dataset, nil)
		if err != nil {
			return nil, err
		}

		p.Dataset.Commit = &dataset.Commit{
			RunID: runID,
		}
	}

	res, err := datasetImpl{}.Save(scope, &SaveParams{
		Ref:     p.Ref,
		Dataset: p.Dataset,
		Apply:   false, // explicityly NOT applying here, script is already run
	})
	if err != nil {
		if errors.Is(err, dsfs.ErrNoChanges) {
			err = nil
		} else {
			log.Errorw("deploy save dataset", "error", err)
			return nil, err
		}
	}

	p.Workflow.OwnerID = scope.ActiveProfile().ID
	p.Workflow.DatasetID = res.ID
	p.Workflow.Deployed = true
	log.Warnw("saving workflow", "workflow", p.Workflow)

	wf, err := scope.Automation().SaveWorkflow(scope.ctx, p.Workflow)
	if err != nil {
		return nil, err
	}

	ref := dsref.ConvertDatasetToVersionInfo(res).SimpleRef()

	return &DeployResponse{
		Ref:      ref.String(),
		RunID:    runID,
		Workflow: wf,
	}, nil
}

func (inst *Instance) apply(ctx context.Context, wait bool, runID string, wf *workflow.Workflow, ds *dataset.Dataset, secrets map[string]string) error {
	// TODO(b5): hack to get a loader for apply
	username := inst.cfg.Profile.Peername
	loader := newDatasetLoader(inst, username, "", false)

	transformer := transform.NewTransformer(inst.appCtx, loader, inst.bus)
	return transformer.Apply(ctx, ds, runID, wait, nil, secrets)
}
