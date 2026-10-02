package devmcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"trace-loop/os/storage/dbexplorer"
)

func registerDBTools(srv *mcp.Server, ex *dbexplorer.Explorer) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "db_list_tables",
		Description: "List every table in the app's database, with engine and an approximate row count.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listTablesOut, error) {
		tables, err := ex.ListTables(ctx)
		if err != nil {
			return nil, listTablesOut{}, err
		}
		return nil, listTablesOut{Tables: tables}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "db_describe_table",
		Description: "Describe a table's columns, indexes, and foreign keys.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in describeTableIn) (*mcp.CallToolResult, dbexplorer.TableSchema, error) {
		schema, err := ex.DescribeTable(ctx, in.Table)
		if err != nil {
			return nil, dbexplorer.TableSchema{}, err
		}
		return nil, *schema, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "db_query",
		Description: "Run a read-only SQL statement (SELECT/SHOW/DESCRIBE/EXPLAIN/WITH) and return the result rows. A LIMIT is added automatically if the statement doesn't include one.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sqlIn) (*mcp.CallToolResult, dbexplorer.QueryResult, error) {
		res, err := ex.Query(ctx, in.SQL)
		if err != nil {
			return nil, dbexplorer.QueryResult{}, err
		}
		return nil, *res, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "db_explain",
		Description: "Return the query planner's EXPLAIN output for a SELECT/WITH statement.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sqlIn) (*mcp.CallToolResult, dbexplorer.QueryResult, error) {
		res, err := ex.Explain(ctx, in.SQL)
		if err != nil {
			return nil, dbexplorer.QueryResult{}, err
		}
		return nil, *res, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "db_sample_rows",
		Description: "Return a random sample of rows from a table.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sampleRowsIn) (*mcp.CallToolResult, dbexplorer.QueryResult, error) {
		res, err := ex.SampleRows(ctx, in.Table, in.Limit)
		if err != nil {
			return nil, dbexplorer.QueryResult{}, err
		}
		return nil, *res, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "db_insert_test_row",
		Description: "Insert one row into a table for test/fixture data. Column values are bound as query parameters. Disabled unless mutations are explicitly enabled (DEV_DB_ALLOW_MUTATIONS=true) and the app is not running in production.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in insertTestRowIn) (*mcp.CallToolResult, dbexplorer.InsertResult, error) {
		res, err := ex.InsertTestRow(ctx, in.Table, in.Values)
		if err != nil {
			return nil, dbexplorer.InsertResult{}, err
		}
		return nil, *res, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "db_reset_test_data",
		Description: "Delete every row in a table (no WHERE clause) so a test fixture can be rebuilt from empty. Disabled unless mutations are explicitly enabled (DEV_DB_ALLOW_MUTATIONS=true) and the app is not running in production.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in tableIn) (*mcp.CallToolResult, dbexplorer.ResetResult, error) {
		res, err := ex.ResetTestData(ctx, in.Table)
		if err != nil {
			return nil, dbexplorer.ResetResult{}, err
		}
		return nil, *res, nil
	})
}

type listTablesOut struct {
	Tables []dbexplorer.Table `json:"tables"`
}

type tableIn struct {
	Table string `json:"table" jsonschema:"table name"`
}

type describeTableIn struct {
	Table string `json:"table" jsonschema:"table name to describe"`
}

type sqlIn struct {
	SQL string `json:"sql" jsonschema:"a single read-only SQL statement (SELECT, SHOW, DESCRIBE, EXPLAIN, or WITH)"`
}

type sampleRowsIn struct {
	Table string `json:"table" jsonschema:"table name to sample"`
	Limit int    `json:"limit,omitempty" jsonschema:"max rows to return (default 20, max 1000)"`
}

type insertTestRowIn struct {
	Table  string         `json:"table" jsonschema:"table name to insert into"`
	Values map[string]any `json:"values" jsonschema:"column name to value for the new row"`
}
