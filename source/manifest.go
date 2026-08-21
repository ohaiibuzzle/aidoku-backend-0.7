package source

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/ohaiibuzzle/aidokurunner-go/models"
)

// ManifestInfo is the subset of a source's static manifest (source.json)
// needed to identify and display it without loading its WASM module -- see
// ReadManifest.
type ManifestInfo struct {
	Key       string
	Name      string
	Version   int
	Languages []string
}

// ReadManifest reads just source.json from path (a plain directory or a
// packaged .aix/.zip archive), without touching main.wasm or instantiating
// a runtime.Interpreter -- unlike LoadPath, which needs both to do
// anything else. For callers that only need a source's identity (Key) and
// display name (e.g. listing installed sources), this is a plain zip-open
// plus JSON-unmarshal instead of standing up a full QuickJS runtime, so
// it's cheap enough to call once per installed source on every listing
// rather than needing its own caching layer.
func ReadManifest(path string) (*ManifestInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("source: stat %s: %w", path, err)
	}

	var data []byte
	if info.IsDir() {
		data, err = os.ReadFile(filepath.Join(path, "source.json"))
		if err != nil {
			return nil, fmt.Errorf("source: reading source.json: %w", err)
		}
	} else {
		r, err := zip.OpenReader(path)
		if err != nil {
			return nil, fmt.Errorf("source: opening %s: %w", path, err)
		}
		defer r.Close()
		sub, err := fs.Sub(r, "Payload")
		if err != nil {
			return nil, fmt.Errorf("source: %s has no Payload/ directory: %w", path, err)
		}
		data, err = fs.ReadFile(sub, "source.json")
		if err != nil {
			return nil, fmt.Errorf("source: reading source.json: %w", err)
		}
	}

	var raw models.SourceInfo
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("source: parsing source.json: %w", err)
	}
	return &ManifestInfo{
		Key:       raw.Info.ID,
		Name:      raw.Info.Name,
		Version:   raw.Info.Version,
		Languages: raw.Info.Languages,
	}, nil
}
