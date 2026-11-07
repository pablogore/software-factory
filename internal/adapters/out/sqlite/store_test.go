package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/adapters/out/sqlite"
)

// openStore opens a SQLite store in a fresh file, closed when the test ends.
func openStore(ctx *specs.Context, opts ...sqlite.Option) *sqlite.Store {
	st, err := sqlite.Open(context.Background(), filepath.Join(ctx.Testing().TempDir(), "factory.db"), opts...)
	ctx.Expect(err).To(specs.BeNil())
	ctx.Cleanup(func() { _ = st.Close() })
	return st
}

// newMemstore returns an empty in-memory fake.
func newMemstore(ctx *specs.Context, opts ...memstore.Option) *memstore.Store {
	st, err := memstore.New(opts...)
	ctx.Expect(err).To(specs.BeNil())
	return st
}

func TestStore(t *testing.T) {
	specs.Describe(t, "sqlite.Store", func(s *specs.Spec) {
		storeContract(s, func(ctx *specs.Context) store { return openStore(ctx) })
	})
}

func TestMemstore(t *testing.T) {
	specs.Describe(t, "memstore.Store (fake)", func(s *specs.Spec) {
		storeContract(s, func(ctx *specs.Context) store { return newMemstore(ctx) })
	})
}
