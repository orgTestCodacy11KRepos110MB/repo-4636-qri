package test

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"

	"github.com/qri-io/qri/auth/key"
	testkeys "github.com/qri-io/qri/auth/key/test"
)

// NewTestCrypto returns a mocked cryptographic generator for tests
func NewTestCrypto() key.CryptoGenerator {
	return &testCryptoGenerator{}
}

var _ key.CryptoGenerator = (*testCryptoGenerator)(nil)

type testCryptoGenerator struct {
	count int
}

func (g *testCryptoGenerator) GeneratePrivateKeyAndPeerID() (string, string) {
	kd := testkeys.GetKeyData(g.count)
	g.count++
	return kd.EncodedPrivKey, kd.EncodedPeerID
}

// InitIPFSRepo creates an IPFS repo by un-zipping a preconstructed IPFS repo
func InitIPFSRepo(repoPath, configPath string) error {
	unzipFile(TestdataPath("empty_ipfs_repo.zip"), repoPath)
	return nil
}

func unzipFile(sourceZip, destDir string) {
	r, err := zip.OpenReader(sourceZip)
	if err != nil {
		panic(err)
	}
	defer r.Close()

	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			panic(err)
		}
		defer rc.Close()

		fpath := filepath.Join(destDir, f.Name)
		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
		} else {
			if err = os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
				panic(err)
			}
			outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
			if err != nil {
				panic(err)
			}
			_, err = io.Copy(outFile, rc)
			outFile.Close()
		}
	}
}
