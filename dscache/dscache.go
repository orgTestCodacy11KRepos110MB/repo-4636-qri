package dscache

import (
	"context"
	"fmt"
	"io/ioutil"
	"strings"
	"time"

	flatbuffers "github.com/google/flatbuffers/go"
	golog "github.com/ipfs/go-log"
	"github.com/qri-io/dataset"
	"github.com/qri-io/qfs"
	"github.com/qri-io/qri/dscache/dscachefb"
	"github.com/qri-io/qri/dsref"
	"github.com/qri-io/qri/event"
	"github.com/qri-io/qri/profile"
	reporef "github.com/qri-io/qri/repo/ref"
)

var (
	log = golog.Logger("dscache")
	// lengthOfProfileID is the expected length of a valid profileID
	lengthOfProfileID = 46
	// ErrNoDscache is returned when methods are called on a non-existant Dscache
	ErrNoDscache = fmt.Errorf("dscache: does not exist")
	// ErrInvalidProfileID is returned when an invalid profileID is given to dscache
	ErrInvalidProfileID = fmt.Errorf("invalid profileID")
)

// Dscache represents an in-memory serialized dscache flatbuffer
type Dscache struct {
	Filename            string
	Root                *dscachefb.Dscache
	Buffer              []byte
	CreateNewEnabled    bool
	ProfileIDToUsername map[string]string
	DefaultUsername     string
}

// NewDscache will construct a dscache from the given filename, or will construct an empty dscache
// that will save to the given filename. Using an empty filename will disable loading and saving
func NewDscache(ctx context.Context, fsys qfs.Filesystem, bus event.Bus, username, filename string) *Dscache {
	cache := Dscache{Filename: filename}
	f, err := fsys.Get(ctx, filename)
	if err == nil {
		// Ignore error, as dscache loading is optional
		defer f.Close()
		buffer, err := ioutil.ReadAll(f)
		if err != nil {
			log.Error(err)
		} else {
			root := dscachefb.GetRootAsDscache(buffer, 0)
			cache = Dscache{Filename: filename, Root: root, Buffer: buffer}
		}
	}
	cache.DefaultUsername = username
	bus.SubscribeTypes(cache.handler,
		event.ETDatasetNameInit,
		event.ETLogbookWriteCommit,
		event.ETDatasetDeleteAll,
		event.ETDatasetRename,
		event.ETDatasetCreateLink)

	return &cache
}

// IsEmpty returns whether the dscache has any constructed data in it
func (d *Dscache) IsEmpty() bool {
	if d == nil {
		return true
	}
	return d.Root == nil
}

// Assign assigns the data from one dscache to this one
func (d *Dscache) Assign(other *Dscache) error {
	if d == nil {
		return ErrNoDscache
	}
	d.Root = other.Root
	d.Buffer = other.Buffer
	return d.save()
}

// VerboseString is a convenience function that returns a readable string, for testing and debugging
func (d *Dscache) VerboseString(showEmpty bool) string {
	if d.IsEmpty() {
		return "dscache: cannot not stringify an empty dscache"
	}
	out := strings.Builder{}
	out.WriteString("Dscache:\n")
	out.WriteString(" Dscache.Users:\n")
	for i := 0; i < d.Root.UsersLength(); i++ {
		userAssoc := dscachefb.UserAssoc{}
		d.Root.Users(&userAssoc, i)
		username := userAssoc.Username()
		profileID := userAssoc.ProfileID()
		fmt.Fprintf(&out, " %2d) user=%s profileID=%s\n", i, username, profileID)
	}
	out.WriteString(" Dscache.Refs:\n")
	for i := 0; i < d.Root.RefsLength(); i++ {
		r := dscachefb.RefEntryInfo{}
		d.Root.Refs(&r, i)
		fmt.Fprintf(&out, ` %2d) initID        = %s
     profileID     = %s
     topIndex      = %d
     cursorIndex   = %d
     prettyName    = %s
`, i, r.InitID(), r.ProfileID(), r.TopIndex(), r.CursorIndex(), r.PrettyName())
		indent := "     "
		if len(r.MetaTitle()) != 0 || showEmpty {
			fmt.Fprintf(&out, "%smetaTitle     = %s\n", indent, r.MetaTitle())
		}
		if len(r.ThemeList()) != 0 || showEmpty {
			fmt.Fprintf(&out, "%sthemeList     = %s\n", indent, r.ThemeList())
		}
		if r.BodySize() != 0 || showEmpty {
			fmt.Fprintf(&out, "%sbodySize      = %d\n", indent, r.BodySize())
		}
		if r.BodyRows() != 0 || showEmpty {
			fmt.Fprintf(&out, "%sbodyRows      = %d\n", indent, r.BodyRows())
		}
		if r.CommitTime() != 0 || showEmpty {
			fmt.Fprintf(&out, "%scommitTime    = %d\n", indent, r.CommitTime())
		}
		if r.NumErrors() != 0 || showEmpty {
			fmt.Fprintf(&out, "%snumErrors     = %d\n", indent, r.NumErrors())
		}
		if len(r.HeadRef()) != 0 || showEmpty {
			fmt.Fprintf(&out, "%sheadRef       = %s\n", indent, r.HeadRef())
		}
	}
	return out.String()
}

// ListRefs returns references to each dataset in the cache
func (d *Dscache) ListRefs() ([]reporef.DatasetRef, error) {
	if d.IsEmpty() {
		return nil, ErrNoDscache
	}
	d.ensureProToUserMap()
	refs := make([]reporef.DatasetRef, 0, d.Root.RefsLength())
	for i := 0; i < d.Root.RefsLength(); i++ {
		refCache := dscachefb.RefEntryInfo{}
		d.Root.Refs(&refCache, i)

		proIDStr := string(refCache.ProfileID())
		profileID, err := profile.IDB58Decode(proIDStr)
		if err != nil {
			log.Errorf("could not parse profileID %q", proIDStr)
		}
		username, ok := d.ProfileIDToUsername[proIDStr]
		if !ok {
			log.Errorf("no username associated with profileID %q", proIDStr)
		}

		refs = append(refs, reporef.DatasetRef{
			Peername:  username,
			ProfileID: profileID,
			Name:      string(refCache.PrettyName()),
			Path:      string(refCache.HeadRef()),
			FSIPath:   string(refCache.FsiPath()),
			Dataset: &dataset.Dataset{
				Meta: &dataset.Meta{
					Title: string(refCache.MetaTitle()),
				},
				Structure: &dataset.Structure{
					ErrCount: int(refCache.NumErrors()),
					Entries:  int(refCache.BodyRows()),
					Length:   int(refCache.BodySize()),
				},
				Commit:      &dataset.Commit{},
				NumVersions: int(refCache.TopIndex()),
			},
		})
	}
	return refs, nil
}

// ResolveRef completes a reference using available data, filling in either
// missing initID or human fields
// implements dsref.Resolver interface
func (d *Dscache) ResolveRef(ctx context.Context, ref *dsref.Ref) (string, error) {
	// NOTE: isEmpty is nil-callable. important b/c ResolveRef must be nil-callable
	if d.IsEmpty() {
		return "", dsref.ErrRefNotFound
	}

	if ref.InitID != "" {
		return d.completeRef(ctx, ref)
	}

	vi, err := d.LookupByName(*ref)
	if err != nil {
		return "", dsref.ErrRefNotFound
	}

	ref.InitID = vi.InitID
	ref.ProfileID = vi.ProfileID
	if ref.Path == "" {
		ref.Path = vi.Path
	}

	return "", nil
}

func (d *Dscache) completeRef(ctx context.Context, ref *dsref.Ref) (string, error) {

	r := dscachefb.RefEntryInfo{}
	for i := 0; i < d.Root.RefsLength(); i++ {
		d.Root.Refs(&r, i)
		if string(r.InitID()) == ref.InitID {
			ref.Path = string(r.HeadRef())
			ref.ProfileID = string(r.ProfileID())
			ref.Name = string(r.PrettyName())

			// Convert profileID into a username
			for i := 0; i < d.Root.UsersLength(); i++ {
				userAssoc := dscachefb.UserAssoc{}
				d.Root.Users(&userAssoc, i)
				username := userAssoc.Username()
				profileID := userAssoc.ProfileID()
				if string(profileID) == ref.ProfileID {
					ref.Username = string(username)
					break
				}
			}

			return "", nil
		}
	}

	return "", dsref.ErrRefNotFound
}

// LookupByName looks up a dataset by dsref and returns the latest VersionInfo if found
func (d *Dscache) LookupByName(ref dsref.Ref) (*dsref.VersionInfo, error) {
	// Convert the username into a profileID
	for i := 0; i < d.Root.UsersLength(); i++ {
		userAssoc := dscachefb.UserAssoc{}
		d.Root.Users(&userAssoc, i)
		username := userAssoc.Username()
		profileID := userAssoc.ProfileID()
		if ref.Username == string(username) {
			// TODO(dustmop): Switch off of profileID to a stable ID (that handle key rotations)
			// based upon the Logbook creation of a user's profile.
			ref.ProfileID = string(profileID)
			break
		}
	}
	if ref.ProfileID == "" {
		return nil, fmt.Errorf("unknown username %q", ref.Username)
	}
	// Lookup the info, given the profileID/dsname
	for i := 0; i < d.Root.RefsLength(); i++ {
		r := dscachefb.RefEntryInfo{}
		d.Root.Refs(&r, i)
		if string(r.ProfileID()) == ref.ProfileID && string(r.PrettyName()) == ref.Name {
			info := convertEntryToVersionInfo(&r)
			return &info, nil
		}
	}
	return nil, fmt.Errorf("dataset ref not found %s/%s", ref.Username, ref.Name)
}

func (d *Dscache) validateProfileID(profileID string) bool {
	return len(profileID) == lengthOfProfileID
}

func (d *Dscache) handler(_ context.Context, e event.Event) error {
	switch e.Type {
	case event.ETDatasetNameInit:
		act, ok := e.Payload.(dsref.VersionInfo)
		if !ok {
			log.Error("dscache got an event with a payload that isn't a dsref.VersionInfo type: %v", e.Payload)
			return nil
		}
		if err := d.updateInitDataset(act); err != nil && err != ErrNoDscache {
			log.Error(err)
		}
	case event.ETLogbookWriteCommit:
		act, ok := e.Payload.(dsref.VersionInfo)
		if !ok {
			log.Error("dscache got an event with a payload that isn't a dsref.VersionInfo type: %v", e.Payload)
			return nil
		}
		if err := d.updateChangeCursor(act); err != nil && err != ErrNoDscache {
			log.Error(err)
		}
	case event.ETDatasetDeleteAll:
		initID, ok := e.Payload.(string)
		if !ok {
			log.Error("dscache got an event with a payload that isn't a string type: %v", e.Payload)
			return nil
		}
		if err := d.updateDeleteDataset(initID); err != nil && err != ErrNoDscache {
			log.Error(err)
		}
	case event.ETDatasetRename:
		// TODO(dustmop): Handle renames
	}

	return nil
}

func (d *Dscache) updateInitDataset(act dsref.VersionInfo) error {
	if d.IsEmpty() {
		// Only create a new dscache if that feature is enabled. This way no one is forced to
		// use dscache without opting in.
		if !d.CreateNewEnabled {
			return nil
		}

		if !d.validateProfileID(act.ProfileID) {
			return ErrInvalidProfileID
		}

		builder := NewBuilder()
		builder.AddUser(act.Username, act.ProfileID)
		builder.AddDsVersionInfo(dsref.VersionInfo{
			InitID:    act.InitID,
			ProfileID: act.ProfileID,
			Name:      act.Name,
		})
		cache := builder.Build()
		d.Assign(cache)
		return nil
	}
	builder := NewBuilder()
	// copy users
	for i := 0; i < d.Root.UsersLength(); i++ {
		up := dscachefb.UserAssoc{}
		d.Root.Users(&up, i)
		builder.AddUser(string(up.Username()), string(up.ProfileID()))
	}
	// copy ds versions
	for i := 0; i < d.Root.UsersLength(); i++ {
		r := dscachefb.RefEntryInfo{}
		d.Root.Refs(&r, i)
		builder.AddDsVersionInfoWithIndexes(convertEntryToVersionInfo(&r), int(r.TopIndex()), int(r.CursorIndex()))
	}
	// Add new ds version info
	builder.AddDsVersionInfo(dsref.VersionInfo{
		InitID:    act.InitID,
		ProfileID: act.ProfileID,
		Name:      act.Name,
	})
	cache := builder.Build()
	d.Assign(cache)
	return nil
}

// Copy the entire dscache, except for the matching entry, rebuild that one to modify it
func (d *Dscache) updateChangeCursor(act dsref.VersionInfo) error {
	if d.IsEmpty() {
		return ErrNoDscache
	}
	// Flatbuffers for go do not allow mutation (for complex types like strings). So we construct
	// a new flatbuffer entirely, copying the old one while replacing the entry we care to change.
	builder := flatbuffers.NewBuilder(0)
	users := d.copyUserAssociationList(builder)
	refs := d.copyReferenceListWithReplacement(
		builder,
		// Function to match the entry we're looking to replace
		func(r *dscachefb.RefEntryInfo) bool {
			return string(r.InitID()) == act.InitID
		},
		// Function to replace the matching entry
		func(refStartMutationFunc func(builder *flatbuffers.Builder)) {
			var metaTitle flatbuffers.UOffsetT
			metaTitle = builder.CreateString(act.MetaTitle)
			hashRef := builder.CreateString(string(act.Path))
			// Start building a ref object, by mutating an existing ref object.
			refStartMutationFunc(builder)
			// Add only the fields we want to change.
			dscachefb.RefEntryInfoAddTopIndex(builder, int32(act.CommitCount))
			dscachefb.RefEntryInfoAddCursorIndex(builder, int32(act.CommitCount))
			dscachefb.RefEntryInfoAddMetaTitle(builder, metaTitle)
			dscachefb.RefEntryInfoAddCommitTime(builder, act.CommitTime.Unix())
			dscachefb.RefEntryInfoAddBodySize(builder, int64(act.BodySize))
			dscachefb.RefEntryInfoAddBodyRows(builder, int32(act.BodyRows))
			dscachefb.RefEntryInfoAddNumErrors(builder, int32(act.NumErrors))
			dscachefb.RefEntryInfoAddHeadRef(builder, hashRef)
			// Don't call RefEntryInfoEnd, that is handled by copyReferenceListWithReplacement
		},
	)
	root, serialized := d.finishBuilding(builder, users, refs)
	d.Root = root
	d.Buffer = serialized
	return d.save()
}

// Copy the entire dscache, except leave out the matching entry.
func (d *Dscache) updateDeleteDataset(initID string) error {
	if d.IsEmpty() {
		return ErrNoDscache
	}
	// Flatbuffers for go do not allow mutation (for complex types like strings). So we construct
	// a new flatbuffer entirely, copying the old one while omitting the entry we want to remove.
	builder := flatbuffers.NewBuilder(0)
	users := d.copyUserAssociationList(builder)
	refs := d.copyReferenceListWithReplacement(
		builder,
		func(r *dscachefb.RefEntryInfo) bool {
			return string(r.InitID()) == initID
		},
		// Pass a nil function, so the matching entry is not replaced, it is omitted
		nil,
	)
	root, serialized := d.finishBuilding(builder, users, refs)
	d.Root = root
	d.Buffer = serialized
	return d.save()
}

func convertEntryToVersionInfo(r *dscachefb.RefEntryInfo) dsref.VersionInfo {
	return dsref.VersionInfo{
		InitID:      string(r.InitID()),
		ProfileID:   string(r.ProfileID()),
		Name:        string(r.PrettyName()),
		Path:        string(r.HeadRef()),
		Published:   r.Published(),
		Foreign:     r.Foreign(),
		MetaTitle:   string(r.MetaTitle()),
		ThemeList:   string(r.ThemeList()),
		BodySize:    int(r.BodySize()),
		BodyRows:    int(r.BodyRows()),
		BodyFormat:  string(r.BodyFormat()),
		NumErrors:   int(r.NumErrors()),
		CommitTime:  time.Unix(r.CommitTime(), 0),
		CommitCount: int(r.CommitCount()),
	}
}

func (d *Dscache) ensureProToUserMap() {
	if d.ProfileIDToUsername != nil {
		return
	}
	d.ProfileIDToUsername = make(map[string]string)
	for i := 0; i < d.Root.UsersLength(); i++ {
		userAssoc := dscachefb.UserAssoc{}
		d.Root.Users(&userAssoc, i)
		username := userAssoc.Username()
		profileID := userAssoc.ProfileID()
		d.ProfileIDToUsername[string(profileID)] = string(username)
	}
}

// save writes the serialized bytes to the given filename
func (d *Dscache) save() error {
	if d.Filename == "" {
		log.Infof("dscache: no filename set, will not save")
		return nil
	}
	return ioutil.WriteFile(d.Filename, d.Buffer, 0644)
}
