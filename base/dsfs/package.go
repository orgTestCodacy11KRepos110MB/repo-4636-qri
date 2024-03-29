package dsfs

import (
	"fmt"
	"strings"

	"github.com/qri-io/qfs"
	"github.com/qri-io/qfs/muxfs"
)

const (
	// transformScriptFilename is the name transform scripts will be written to
	transformScriptFilename = "transform_script"
	// vizsScriptFilename is the name transform scripts will be written to
	vizScriptFilename = "viz_script"
	// readmeScriptFilename is the name of the readme file that will be written to
	readmeScriptFilename = "readme.json"
)

// PackageFile specifies the different types of files that are
// stored in a package
type PackageFile int

const (
	// PackageFileUnknown is the default package file, which
	// should be erroneous, as there is no sensible default
	// for PackageFile
	PackageFileUnknown PackageFile = iota
	// PackageFileDataset is the main dataset.json file
	// that contains all dataset metadata, and is the only
	// required file to constitute a dataset
	PackageFileDataset
	// PackageFileStructure isolates this dataset's structure
	// in it's own file
	PackageFileStructure
	// PackageFileAbstract is the abstract verion of
	// structure
	PackageFileAbstract
	// PackageFileResources lists the resource datasets
	// that went into creating a dataset
	// TODO - I think this can be removed now that Transform exists
	PackageFileResources
	// PackageFileCommit isolates the user-entered
	// documentation of the changes to this dataset's history
	PackageFileCommit
	// PackageFileTransform isloates the concrete transform that
	// generated this dataset
	PackageFileTransform
	// PackageFileAbstractTransform is the abstract version of
	// the operation performed to create this dataset
	PackageFileAbstractTransform
	// PackageFileMeta encapsulates human-readable metadata
	PackageFileMeta
	// PackageFileViz isolates the data related to representing a dataset as a
	// visualization
	PackageFileViz
	// PackageFileVizScript is the viz template
	PackageFileVizScript
	// PackageFileRenderedViz is the rendered visualization of the dataset
	PackageFileRenderedViz
	// PackageFileReadme connects readme data to the dataset package
	PackageFileReadme
	// PackageFileReadmeScript is the raw readme of the dataset
	PackageFileReadmeScript
	// PackageFileRenderedReadme is the rendered readme of the dataset
	PackageFileRenderedReadme
	// PackageFileStats isolates the statistical metadata component
	PackageFileStats
)

// filenames maps PackageFile to their filename counterparts
var filenames = map[PackageFile]string{
	PackageFileUnknown:           "",
	PackageFileDataset:           "dataset.json",
	PackageFileStructure:         "structure.json",
	PackageFileAbstract:          "abstract.json",
	PackageFileAbstractTransform: "abstract_transform.json",
	PackageFileResources:         "resources",
	PackageFileCommit:            "commit.json",
	PackageFileTransform:         "transform.json",
	PackageFileMeta:              "meta.json",
	PackageFileViz:               "viz.json",
	PackageFileVizScript:         "viz_script",
	PackageFileRenderedViz:       "index.html",
	PackageFileReadme:            "readme.json",
	PackageFileReadmeScript:      "readme.md",
	PackageFileRenderedReadme:    "readme.html",
	PackageFileStats:             "stats.json",
}

// String implements the io.Stringer interface for PackageFile
func (p PackageFile) String() string {
	return filenames[p]
}

// Filename gives the canonical filename for a PackageFile
func (p PackageFile) Filename() string {
	return fmt.Sprintf("/%s", filenames[p])
}

// GetHashBase strips paths to return just the hash
func GetHashBase(in string) string {
	in = strings.TrimLeft(in, "/")
	for _, fsType := range muxfs.KnownFSTypes() {
		in = strings.TrimPrefix(in, fsType)
	}
	in = strings.TrimLeft(in, "/")
	return strings.Split(in, "/")[0]
}

// PackageFilepath returns the path to a package file for a given base path
// It relies relies on package storage conventions and qfs.Filesystem path prefixes
// If you supply a path that does not match the filestore's naming conventions will
// return an invalid path
func PackageFilepath(fs qfs.Filesystem, path string, pf PackageFile) string {
	prefix := fs.Type()
	if prefix == muxfs.FilestoreType {
		// TODO(b5) - for situations where a muxfs is passed, we rely on path being populated
		// with the desired filesystem resolver intact. This should be hardened
		return strings.Join([]string{path, pf.String()}, "/")
	}

	if prefix == "" {
		return path
	}
	// Keep forward slashes in the path by using strings.Join instead of filepath.Join. This
	// will make IPFS happy on Windows, since it always wants "/" and not "\". The blank
	// path component in the front of this join ensures that the path begins with a "/" character.
	return strings.Join([]string{"", prefix, GetHashBase(path), pf.String()}, "/")
}
