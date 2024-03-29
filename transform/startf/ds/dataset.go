// Package ds exposes the qri dataset document model into starlark
package ds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"sort"
	"strings"
	"sync"

	golog "github.com/ipfs/go-log"
	"github.com/qri-io/dataset"
	"github.com/qri-io/dataset/detect"
	"github.com/qri-io/dataset/dsio"
	"github.com/qri-io/dataset/tabular"
	"github.com/qri-io/qfs"
	"github.com/qri-io/qri/base"
	"github.com/qri-io/qri/base/dsfs"
	"github.com/qri-io/qri/dsref"
	"github.com/qri-io/starlib/dataframe"
	"github.com/qri-io/starlib/util"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

var log = golog.Logger("stards")

// ModuleName defines the expected name for this Module when used
// in starlark's load() function, eg: load('dataset.star', 'dataset')
const ModuleName = "dataset.star"

var (
	once          sync.Once
	datasetModule starlark.StringDict
)

// LoadModule loads the base64 module.
// It is concurrency-safe and idempotent.
func LoadModule() (starlark.StringDict, error) {
	once.Do(func() {
		datasetModule = starlark.StringDict{
			"dataset": starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
				"new": starlark.NewBuiltin("new", New),
			}),
		}
	})
	return datasetModule, nil
}

// Dataset is a qri dataset starlark type
type Dataset struct {
	frozen    bool
	ds        *dataset.Dataset
	bodyFrame starlark.Value
	changes   map[string]struct{}
	outconf   *dataframe.OutputConfig
}

// compile-time interface assertions
var (
	_ starlark.Value       = (*Dataset)(nil)
	_ starlark.HasAttrs    = (*Dataset)(nil)
	_ starlark.HasSetField = (*Dataset)(nil)
	_ starlark.Unpacker    = (*Dataset)(nil)
)

// methods defined on the dataset object
var dsMethods = map[string]*starlark.Builtin{
	"set_meta":      starlark.NewBuiltin("set_meta", dsSetMeta),
	"get_meta":      starlark.NewBuiltin("get_meta", dsGetMeta),
	"get_structure": starlark.NewBuiltin("get_structure", dsGetStructure),
	"set_structure": starlark.NewBuiltin("set_structure", dsSetStructure),
}

// NewDataset creates a dataset object, intended to be called from go-land to prepare datasets
// for handing to other functions
func NewDataset(ds *dataset.Dataset, outconf *dataframe.OutputConfig) *Dataset {
	return &Dataset{ds: ds, outconf: outconf, changes: make(map[string]struct{})}
}

// New creates a new dataset from starlark land
func New(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	// TODO(dustmop): Add a function to starlib/dataframe that returns this,
	// use that instead. That way all uses of the thread local data stay in
	// that package, instead of leaking out here.
	outconf, _ := thread.Local("OutputConfig").(*dataframe.OutputConfig)
	d := &Dataset{ds: &dataset.Dataset{}, outconf: outconf, changes: make(map[string]struct{})}
	return d, nil
}

// Unpack implements the starlark.Unpacker interface for unpacking starlark
// arguments
func (d *Dataset) Unpack(v starlark.Value) error {
	ds, ok := v.(*Dataset)
	if !ok {
		return fmt.Errorf("expected dataset, got: %s", v.Type())
	}
	*d = *ds
	return nil
}

// Changes returns a map of which components have been changed
func (d *Dataset) Changes() map[string]struct{} {
	return d.changes
}

// Dataset exposes the internal dataset pointer
func (d *Dataset) Dataset() *dataset.Dataset { return d.ds }

// String returns the Dataset as a string
func (d *Dataset) String() string {
	return d.stringify()
}

// Type returns a short string describing the value's type.
func (Dataset) Type() string { return fmt.Sprintf("%s.Dataset", "dataset") }

// Freeze renders Dataset immutable.
func (d *Dataset) Freeze() { d.frozen = true }

// Hash cannot be used with Dataset
func (d *Dataset) Hash() (uint32, error) {
	return 0, fmt.Errorf("unhashable: %s", d.Type())
}

// Truth converts the dataset into a bool
func (d *Dataset) Truth() starlark.Bool {
	return true
}

// Attr gets a value for a string attribute
func (d *Dataset) Attr(name string) (starlark.Value, error) {
	if name == "body" {
		return d.getBody()
	}
	return builtinAttr(d, name, dsMethods)
}

// AttrNames lists available attributes
func (d *Dataset) AttrNames() []string {
	return append(builtinAttrNames(dsMethods), "body")
}

// SetField assigns to a field of the Dataset
func (d *Dataset) SetField(name string, val starlark.Value) error {
	if d.frozen {
		return fmt.Errorf("cannot set, Dataset is frozen")
	}
	if name == "body" {
		return d.setBody(val)
	}
	return starlark.NoSuchAttrError(name)
}

func (d *Dataset) stringify() string {
	// TODO(dustmop): Improve the stringification of a Dataset
	return "<Dataset>"
}

func builtinAttr(recv starlark.Value, name string, methods map[string]*starlark.Builtin) (starlark.Value, error) {
	b := methods[name]
	if b == nil {
		return nil, nil // no such method
	}
	return b.BindReceiver(recv), nil
}

func builtinAttrNames(methods map[string]*starlark.Builtin) []string {
	names := make([]string, 0, len(methods))
	for name := range methods {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// dsGetMeta gets a dataset meta component
func dsGetMeta(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	self := b.Receiver().(*Dataset)

	if self.ds.Meta == nil {
		return starlark.None, nil
	}

	data, err := json.Marshal(self.ds.Meta)
	if err != nil {
		return starlark.None, err
	}

	jsonData := map[string]interface{}{}
	if err := json.Unmarshal(data, &jsonData); err != nil {
		return starlark.None, err
	}

	return util.Marshal(jsonData)
}

// dsSetMeta sets a dataset meta field
func dsSetMeta(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		keyx starlark.String
		valx starlark.Value
	)
	if err := starlark.UnpackPositionalArgs("set_meta", args, kwargs, 2, &keyx, &valx); err != nil {
		return nil, err
	}
	self := b.Receiver().(*Dataset)

	if self.frozen {
		return starlark.None, fmt.Errorf("cannot call set_meta on frozen dataset")
	}
	self.changes["meta"] = struct{}{}

	key := keyx.GoString()

	val, err := util.Unmarshal(valx)
	if err != nil {
		return nil, err
	}

	if self.ds.Meta == nil {
		self.ds.Meta = &dataset.Meta{}
	}

	return starlark.None, self.ds.Meta.Set(key, val)
}

// dsGetStructure gets a dataset structure component
func dsGetStructure(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	self := b.Receiver().(*Dataset)

	if self.ds.Structure == nil {
		return starlark.None, nil
	}

	data, err := json.Marshal(self.ds.Structure)
	if err != nil {
		return starlark.None, err
	}

	jsonData := map[string]interface{}{}
	if err := json.Unmarshal(data, &jsonData); err != nil {
		return starlark.None, err
	}

	return util.Marshal(jsonData)
}

// SetStructure sets the dataset structure component
func dsSetStructure(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	self := b.Receiver().(*Dataset)

	var valx starlark.Value
	if err := starlark.UnpackPositionalArgs("set_structure", args, kwargs, 1, &valx); err != nil {
		return nil, err
	}

	if self.frozen {
		return starlark.None, fmt.Errorf("cannot call set_structure on frozen dataset")
	}
	self.changes["structure"] = struct{}{}

	val, err := util.Unmarshal(valx)
	if err != nil {
		return starlark.None, err
	}

	if self.ds.Structure == nil {
		self.ds.Structure = &dataset.Structure{}
	}

	data, err := json.Marshal(val)
	if err != nil {
		return starlark.None, err
	}

	err = json.Unmarshal(data, self.ds.Structure)
	return starlark.None, err
}

func (d *Dataset) getBody() (starlark.Value, error) {
	if d.bodyFrame != nil {
		return d.bodyFrame, nil
	}

	bodyfile := d.ds.BodyFile()
	if bodyfile == nil {
		// If no body exists, return an empty data frame
		df, _ := dataframe.NewDataFrame(nil, nil, nil, d.outconf)
		d.bodyFrame = df
		return df, nil
	}

	if d.ds.Structure == nil {
		return starlark.None, fmt.Errorf("error: no structure for dataset")
	}

	// Create columns from the structure, if one exists
	columns := d.createColumnsFromStructure()

	// TODO(dustmop): DataFrame should be able to work with an
	// efficient, streaming body file.
	data, err := ioutil.ReadAll(d.ds.BodyFile())
	if err != nil {
		return starlark.None, err
	}
	d.ds.SetBodyFile(qfs.NewMemfileBytes("body.json", data))

	rr, err := dsio.NewEntryReader(d.ds.Structure, qfs.NewMemfileBytes("body.json", data))
	if err != nil {
		return starlark.None, fmt.Errorf("error allocating data reader: %s", err)
	}

	entries, err := base.ReadEntries(rr)
	if err != nil {
		return starlark.None, err
	}
	rows := [][]interface{}{}
	eachEntry := entries.([]interface{})
	for _, ent := range eachEntry {
		r := ent.([]interface{})
		rows = append(rows, r)
	}

	df, err := dataframe.NewDataFrame(rows, columns, nil, d.outconf)
	if err != nil {
		return nil, err
	}
	d.bodyFrame = df
	return df, nil
}

func (d *Dataset) setBody(val starlark.Value) error {
	df, err := dataframe.NewDataFrame(val, nil, nil, d.outconf)
	if err != nil {
		return err
	}
	d.bodyFrame = df
	d.changes["body"] = struct{}{}
	return nil
}

// writeStructure determines the destination data structure for writing a
// dataset body, falling back to a default json structure based on input values
// if no prior structure exists
func (d *Dataset) writeStructure(data starlark.Value) *dataset.Structure {
	// if the write structure has been set, use that
	if d.ds != nil && d.ds.Structure != nil {
		return d.ds.Structure
	}

	// use a default of json as a last resort
	sch := dataset.BaseSchemaArray
	if data.Type() == "dict" {
		sch = dataset.BaseSchemaObject
	}

	return &dataset.Structure{
		Format: "json",
		Schema: sch,
	}
}

// AssignComponentsFromDataframe looks for changes to the Dataframe body
// and columns, and assigns them to the Dataset's body and structure
func (d *Dataset) AssignComponentsFromDataframe(ctx context.Context, changeSet map[string]struct{}, fs qfs.Filesystem, loader dsref.Loader) error {
	if d.ds == nil {
		return nil
	}

	// assign the structure first. This is necessary because the
	// body writer will use this structure to serialize the new body
	if err := d.assignStructureFromDataframeColumns(); err != nil {
		return err
	}

	// assign body file from the dataframe
	if err := d.assignBodyFromDataframe(); err != nil {
		return err
	}

	// assign details to structure and commit based upon how and
	// whether the body has changed
	_, hasBodyChange := changeSet["body"]
	if err := d.assignStructureAndCommitDetails(ctx, fs, loader, hasBodyChange); err != nil {
		return err
	}
	return nil
}

// AssignBodyFromDataframe converts the DataFrame on the object into
// a proper dataset.bodyfile
func (d *Dataset) assignBodyFromDataframe() error {
	if d.bodyFrame == nil {
		return nil
	}
	df, ok := d.bodyFrame.(*dataframe.DataFrame)
	if !ok {
		return fmt.Errorf("bodyFrame has invalid type %T", d.bodyFrame)
	}

	st := d.ds.Structure
	if st == nil {
		st = &dataset.Structure{
			Format: "csv",
			Schema: tabular.BaseTabularSchema,
		}
	}

	w, err := dsio.NewEntryBuffer(st)
	if err != nil {
		return err
	}

	for i := 0; i < df.NumRows(); i++ {
		w.WriteEntry(dsio.Entry{Index: i, Value: df.Row(i)})
	}
	if err := w.Close(); err != nil {
		return err
	}
	bodyBytes := w.Bytes()
	d.ds.SetBodyFile(qfs.NewMemfileBytes(fmt.Sprintf("body.%s", st.Format), bodyBytes))
	err = detect.Structure(d.ds)
	if err != nil {
		return err
	}
	// adding `Entries` here allows us to know the entry count for
	// transforms that are "applied" but not "commited"
	// "commited" dataset versions get `Entries` and other stats
	// computed at the time the version is saved. also get the
	// `Length` to help generate a commit message
	d.ds.Structure.Entries = df.NumRows()
	d.ds.Structure.Length = len(bodyBytes)

	return nil
}

// load the previous dataset version to get the number of entries
// and assign them to this version's structure
func (d *Dataset) assignStructureAndCommitDetails(ctx context.Context, fs qfs.Filesystem, loader dsref.Loader, hasBodyChange bool) error {
	// get the previous dataset version, if one exists
	var prev *dataset.Dataset
	ref := dsref.ConvertDatasetToVersionInfo(d.Dataset()).SimpleRef()
	if !ref.IsEmpty() {
		var err error
		prev, err = loader.LoadDataset(ctx, ref.Alias())
		if err != nil {
			if errors.Is(err, dsref.ErrNoHistory) || errors.Is(err, dsref.ErrRefNotFound) {
				err = nil
			} else {
				return err
			}
		}
	}

	// calculate the commit title and message
	bodyAct := dsfs.BodyDefault
	if !hasBodyChange {
		bodyAct = dsfs.BodySame
	} else if d.ds.Structure.Length > dsfs.BodySizeSmallEnoughToDiff {
		bodyAct = dsfs.BodyTooBig
	}
	fileHint := d.ds.Transform.ScriptPath
	if strings.HasPrefix(fileHint, "/ipfs/") {
		fileHint = ""
	}
	err := dsfs.EnsureCommitTitleAndMessage(ctx, fs, d.ds, prev, bodyAct, fileHint, false)
	if err != nil && !errors.Is(err, dsfs.ErrNoChanges) {
		return err
	}

	if prev == nil || prev.Structure == nil {
		return nil
	}

	// if the body changed, no need to copy the entries from the
	// previous version
	if hasBodyChange {
		return nil
	}

	if d.ds.Structure == nil {
		// This structure is missing vital data if we need to commit
		// the resulting dataset. However, this codepath should only be
		// hit in two cases:
		// 1) the transform we are applying does not alter the body of
		// the dataset, and the previous dataset was not properly loaded
		// before we called `transform.Commit`. In this case, we would
		// have problems saving the resulting dataset, but we would
		// have bigger errors loading the dataset in the first place
		// 2) the transform we are applying does not alter the body of
		// the dataset, we don't have any previous versions, and we are
		// not expecting to commit the resulting dataset. Since we are
		// not expecting to commit the resulting dataset, we don't have
		// to worry that the structure is only partially filled.
		d.ds.Structure = &dataset.Structure{}
	}
	d.ds.Structure.Entries = prev.Structure.Entries
	return nil
}

func (d *Dataset) assignStructureFromDataframeColumns() error {
	if d.bodyFrame == nil {
		return nil
	}
	df, ok := d.bodyFrame.(*dataframe.DataFrame)
	if !ok {
		return fmt.Errorf("bodyFrame has invalid type %T", d.bodyFrame)
	}

	names, types := df.ColumnNamesTypes()
	if names == nil || types == nil {
		return nil
	}

	cols := make([]interface{}, len(names))
	for i := range names {
		cols[i] = map[string]string{
			"title": names[i],
			"type":  dataframeTypeToQriType(types[i]),
		}
	}

	newSchema := map[string]interface{}{
		"type": "array",
		"items": map[string]interface{}{
			"type":  "array",
			"items": cols,
		},
	}

	if d.ds.Structure == nil {
		d.ds.Structure = &dataset.Structure{
			Format: "csv",
		}
	}

	// TODO(dustmop): Hack to clone the schema object to fix the unit tests.
	// The proper fix is to understand why the above construction doesn't work.
	data, err := json.Marshal(newSchema)
	if err != nil {
		return err
	}
	err = json.Unmarshal(data, &newSchema)
	if err != nil {
		return err
	}
	d.ds.Structure.Schema = newSchema

	return nil
}

func (d *Dataset) createColumnsFromStructure() []string {
	var schema map[string]interface{}
	schema = d.ds.Structure.Schema

	itemsTop := schema["items"]
	itemsArray, ok := itemsTop.(map[string]interface{})
	if !ok {
		return nil
	}

	columnItems := itemsArray["items"]
	columnArray, ok := columnItems.([]interface{})
	if !ok {
		return nil
	}

	result := make([]string, len(columnArray))
	for i, colObj := range columnArray {
		colMap, ok := colObj.(map[string]interface{})
		if !ok {
			return nil
		}

		colTitle, ok := colMap["title"].(string)
		if !ok {
			return nil
		}
		colType, ok := colMap["type"].(string)
		if !ok {
			return nil
		}
		result[i] = colTitle
		// TODO: Perhaps use types to construct dataframe columns.
		// Need a test for that behavior.
		_ = colType
	}

	return result
}

// TODO(dustmop): Probably move this to some more common location
func dataframeTypeToQriType(dfType string) string {
	if dfType == "int64" {
		return "integer"
	} else if dfType == "float64" {
		return "number"
	} else if dfType == "object" {
		// TODO(dustmop): This is only usually going to work
		return "string"
	} else if dfType == "bool" {
		return "boolean"
	} else {
		log.Errorf("unknown type %q tried to convert to qri type", dfType)
		return "object"
	}
}
