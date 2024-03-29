// Package buildrepo initializes a qri repo
package buildrepo

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	golog "github.com/ipfs/go-log"
	"github.com/qri-io/qfs"
	"github.com/qri-io/qfs/muxfs"
	"github.com/qri-io/qri/auth/key"
	"github.com/qri-io/qri/config"
	"github.com/qri-io/qri/dscache"
	"github.com/qri-io/qri/event"
	"github.com/qri-io/qri/logbook"
	"github.com/qri-io/qri/profile"
	"github.com/qri-io/qri/repo"
	fsrepo "github.com/qri-io/qri/repo/fs"
)

var log = golog.Logger("buildrepo")

// Options provides additional fields to new
type Options struct {
	Profiles   profile.Store
	Keystore   key.Store
	Filesystem *muxfs.Mux
	Logbook    *logbook.Book
	Dscache    *dscache.Dscache
	Bus        event.Bus
}

// New is the canonical method for building a repo
func New(ctx context.Context, path string, cfg *config.Config, opts ...func(o *Options)) (repo.Repo, error) {
	o := &Options{}
	for _, opt := range opts {
		opt(o)
	}

	// Don't create a localstore with the empty path, this will use the current directory
	if cfg.Repo.Type == "fs" && cfg.Path() == "" {
		return nil, fmt.Errorf("buildRepo.New using filesystem requires non-empty path")
	}

	var err error
	if o.Keystore == nil {
		log.Debug("buildrepo.New: creating keystore")
		if o.Keystore, err = key.NewStore(cfg); err != nil {
			return nil, err
		}
	}
	if o.Profiles == nil {
		log.Debug("buildrepo.New: creating profiles")
		if o.Profiles, err = profile.NewStore(ctx, cfg, o.Keystore); err != nil {
			return nil, err
		}
	}
	if o.Filesystem == nil {
		log.Debug("buildrepo.New: creating filesystem")
		if o.Filesystem, err = NewFilesystem(ctx, cfg); err != nil {
			return nil, err
		}
	}
	if o.Bus == nil {
		log.Debug("buildrepo.New: creating bus")
		o.Bus = event.NilBus
	}

	pro := o.Profiles.Owner(ctx)

	log.Debug("buildrepo.New: profile %q, %q", pro.Peername, pro.ID)
	switch cfg.Repo.Type {
	case "fs":
		if o.Logbook == nil {
			if o.Logbook, err = newLogbook(o.Filesystem, o.Bus, pro, path); err != nil {
				return nil, err
			}
		}
		if o.Dscache == nil {
			if o.Dscache, err = newDscache(ctx, o.Filesystem, o.Bus, o.Logbook, pro.Peername, path); err != nil {
				return nil, err
			}
		}

		return fsrepo.NewRepo(ctx, path, o.Filesystem, o.Logbook, o.Dscache, o.Profiles, o.Bus)
	case "mem":
		return repo.NewMemRepo(ctx, o.Filesystem, o.Logbook, o.Dscache, o.Profiles, o.Bus)
	default:
		return nil, fmt.Errorf("unknown repo type: %s", cfg.Repo.Type)
	}
}

// NewFilesystem creates a qfs.Filesystem from configuration
func NewFilesystem(ctx context.Context, cfg *config.Config) (*muxfs.Mux, error) {
	qriPath := filepath.Dir(cfg.Path())

	for i, fsCfg := range cfg.Filesystems {
		if fsCfg.Type == "ipfs" {
			if path, ok := fsCfg.Config["path"].(string); ok {
				if !filepath.IsAbs(path) {
					// resolve relative filepaths
					cfg.Filesystems[i].Config["path"] = filepath.Join(qriPath, path)
				}
			}
		}
	}

	if cfg.Repo.Type == "mem" {
		hasMemType := false
		for _, fsCfg := range cfg.Filesystems {
			if fsCfg.Type == "mem" {
				hasMemType = true
			}
		}
		if !hasMemType {
			cfg.Filesystems = append(cfg.Filesystems, qfs.Config{Type: "mem"})
		}
	}

	return muxfs.New(ctx, cfg.Filesystems)
}

func newLogbook(fs qfs.Filesystem, bus event.Bus, pro *profile.Profile, repoPath string) (book *logbook.Book, err error) {
	logbookPath := filepath.Join(repoPath, "logbook.qfb")
	return logbook.NewJournal(*pro, bus, fs, logbookPath)
}

func newDscache(ctx context.Context, fs qfs.Filesystem, bus event.Bus, book *logbook.Book, username, repoPath string) (*dscache.Dscache, error) {
	// This seems to be a bug, the repoPath does not end in "qri" in some tests.
	if !strings.HasSuffix(repoPath, "qri") {
		return nil, fmt.Errorf("invalid repo path: %q", repoPath)
	}
	dscachePath := filepath.Join(repoPath, "dscache.qfb")
	return dscache.NewDscache(ctx, fs, bus, username, dscachePath), nil
}
