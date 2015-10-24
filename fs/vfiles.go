package fs

import (
	"os"
	"path"
	"time"
)

// Vfile or virtual file for provide a virtual file info
type Vfile struct {
	path string
	name string
	data []byte
	mod  time.Time
}

// Path returns the path of the file
func (v *Vfile) Path() string {
	return path.Join(v.path, v.name)
}

// Name returns the name of the file
func (v *Vfile) Name() string {
	return v.name
}

// Sys returns nil
func (v *Vfile) Sys() interface{} {
	return nil
}

// Data returns the data captured within
func (v *Vfile) Data() []byte {
	return v.data
}

// Mode returns 0 as the filemode
func (v *Vfile) Mode() os.FileMode {
	return 0
}

// Size returns the size of the data
func (v *Vfile) Size() int64 {
	return int64(len(v.data))
}

// ModTime returns the modtime for the virtual file
func (v *Vfile) ModTime() time.Time {
	return v.mod
}

// IsDir returns false
func (v *Vfile) IsDir() bool {
	return false
}
