// Package source opens import files. The importer only sees the Source
// interface (IMP-9), so another storage can be added without touching it.
package source

import (
	"context"
	"fmt"
	"io"
	"os"
)

type Source interface {
	Open(ctx context.Context, name string) (io.ReadCloser, error)
}

// Local opens files from the local disk.
type Local struct{}

func (Local) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open local source: %w", err) // err already names the file
	}
	return f, nil
}
