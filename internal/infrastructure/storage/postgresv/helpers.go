package postgresv

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/whiskeyjimbo/para-mcp/internal/core/ports"
	"github.com/whiskeyjimbo/para-mcp/internal/infrastructure/index"
)

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func newIndex() ports.FTSIndex { return index.New() }
