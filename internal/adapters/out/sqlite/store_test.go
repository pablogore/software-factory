package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/adapters/out/sqlite"
)

func TestStore(t *testing.T) {
	specs.Describe(t, "sqlite.Store", func(s *specs.Spec) {
		storeContract(s, func(ctx *specs.Context) store {
			st, err := sqlite.Open(context.Background(), filepath.Join(ctx.Testing().TempDir(), "factory.db"))
			ctx.Expect(err).To(specs.BeNil())
			ctx.Cleanup(func() { _ = st.Close() })
			return st
		})
	})
}

func TestMemstore(t *testing.T) {
	specs.Describe(t, "memstore.Store (fake)", func(s *specs.Spec) {
		storeContract(s, func(*specs.Context) store { return memstore.New() })
	})
}
