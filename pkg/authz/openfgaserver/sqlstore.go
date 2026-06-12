package openfgaserver

import (
	"github.com/SigNoz/signoz/pkg/authz"
	"github.com/SigNoz/signoz/pkg/errors"
	"github.com/SigNoz/signoz/pkg/sqlstore"
	"github.com/SigNoz/signoz/pkg/sqlstore/postgressqlstore"
	"github.com/openfga/openfga/pkg/storage"
	"github.com/openfga/openfga/pkg/storage/postgres"
	"github.com/openfga/openfga/pkg/storage/sqlcommon"
	"github.com/openfga/openfga/pkg/storage/sqlite"
)

func NewSQLStore(store sqlstore.SQLStore, config authz.Config) (storage.OpenFGADatastore, error) {
	switch store.BunDB().Dialect().Name().String() {
	case "sqlite":
		return sqlite.NewWithDB(store.SQLDB(), &sqlcommon.Config{
			MaxTuplesPerWriteField: config.OpenFGA.MaxTuplesPerWrite,
			MaxTypesPerModelField:  100,
		})

	case "pg":
		// Synamedia clean-room addition. The OpenFGA Postgres datastore builds on a
		// *pgxpool.Pool (unlike SQLite, which takes a *sql.DB), so we share the
		// metadata SQLStore's pool via the Pooler interface rather than opening a
		// second pool. The second (read-replica) pool is left nil.
		pooler, ok := store.(postgressqlstore.Pooler)
		if !ok {
			return nil, errors.Newf(errors.TypeInternal, errors.CodeInternal, "postgres sqlstore must implement postgressqlstore.Pooler")
		}

		return postgres.NewWithDB(pooler.Pool(), nil, &sqlcommon.Config{
			MaxTuplesPerWriteField: config.OpenFGA.MaxTuplesPerWrite,
			MaxTypesPerModelField:  100,
		})

	}
	return nil, errors.Newf(errors.TypeInvalidInput, errors.CodeInvalidInput, "invalid store type: %s", store.BunDB().Dialect().Name().String())
}
