package cmd

import (
	"context"
	"testing"

	"github.com/qri-io/qri/base"
	"github.com/qri-io/qri/dsref"
	"github.com/qri-io/qri/errors"
)

func TestRenderComplete(t *testing.T) {
	run := NewTestRunner(t, "test_peer_render_complete", "qri_test_render_complete")
	defer run.Delete()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f, err := NewTestFactory(ctx)
	if err != nil {
		t.Errorf("error creating new test factory: %s", err)
		return
	}

	cases := []struct {
		args   []string
		expect string
		err    string
	}{
		{[]string{}, "", ""},
		{[]string{"test"}, "test", ""},
	}

	for i, c := range cases {
		opt := &RenderOptions{
			IOStreams: run.Streams,
		}

		opt.Complete(f, c.args)

		if c.err != run.ErrStream.String() {
			t.Errorf("case %d, error mismatch. Expected: '%s', Got: '%s'", i, c.err, run.ErrStream.String())
			run.IOReset()
			continue
		}

		if c.expect != opt.Refs.Ref() {
			t.Errorf("case %d, opt.Ref not set correctly. Expected: '%s', Got: '%s'", i, c.expect, opt.Refs.Ref())
			run.IOReset()
			continue
		}

		if opt.inst == nil {
			t.Errorf("case %d, opt.inst not set.", i)
			run.IOReset()
			continue
		}
		run.IOReset()
	}
}

func TestRenderRun(t *testing.T) {
	run := NewTestRunner(t, "test_peer_render_run", "qri_test_render_run")
	defer run.Delete()

	// set Default Template to something easier to work with, then
	// cleanup when test completes
	prevDefaultTemplate := base.DefaultTemplate
	base.DefaultTemplate = `<html><h1>{{ds.peername}}/{{ds.name}}</h1></html>`
	defer func() { base.DefaultTemplate = prevDefaultTemplate }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f, err := NewTestFactory(ctx)
	if err != nil {
		t.Errorf("error creating new test factory: %s", err)
		return
	}

	if err := f.Init(); err != nil {
		t.Errorf("error initializing: %s", err)
		return
	}

	cases := []struct {
		ref      string
		template string
		output   string
		expected string
		err      string
		msg      string
	}{
		{"", "", "", "", dsref.ErrEmptyRef.Error(), "peername and dataset name needed in order to render, for example:\n   $ qri render me/dataset_name\nsee `qri render --help` from more info"},
		{"peer/bad_dataset", "", "", "", "reference not found", `reference "peer/bad_dataset" not found`},
		{"peer/cities", "", "", "<html><h1>peer/cities</h1></html>", "", ""},
		{"peer/cities", "testdata/template.html", "", "<html><h2>peer/cities</h2><tbody><tr><td>toronto</td><td>40000000</td><td>55.5</td><td>false</td></tr><tr><td>new york</td><td>8500000</td><td>44.4</td><td>true</td></tr></tbody></html>", "", ""},
	}

	for i, c := range cases {
		inst, err := f.Instance()
		if err != nil {
			t.Errorf("case %d, error creating instance: %s", i, err)
			continue
		}

		opt := &RenderOptions{
			IOStreams: run.Streams,
			Refs:      NewRefSelect(c.ref),
			UseViz:    true,
			Template:  c.template,
			Output:    c.output,
			inst:      inst,
		}

		err = opt.Run()
		if (err == nil && c.err != "") || (err != nil && c.err != err.Error()) {
			t.Errorf("case %d, mismatched error. Expected: '%s', Got: '%v'", i, c.err, err)
			run.IOReset()
			continue
		}

		if libErr, ok := err.(errors.Error); ok {
			if libErr.Message() != c.msg {
				t.Errorf("case %d, mismatched user-friendly message. Expected: %q, Got: %q", i, c.msg, libErr.Message())
				run.IOReset()
				continue
			}
		} else if c.msg != "" {
			t.Errorf("case %d, mismatched user-friendly message. Expected: %q, Got: ''", i, c.msg)
			run.IOReset()
			continue
		}

		if c.expected != run.OutStream.String() {
			t.Errorf("case %d, output mismatch. Expected: %q, Got: %q", i, c.expected, run.OutStream.String())
			run.IOReset()
			continue
		}
		run.IOReset()
	}
}
