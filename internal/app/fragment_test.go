package app

import (
	"io/fs"
	"testing/fstest"
)

// fragment is an app's Compose files holding only compose.yaml.
func fragment(content string) fs.FS {
	return fstest.MapFS{"compose.yaml": {Data: []byte(content)}}
}
