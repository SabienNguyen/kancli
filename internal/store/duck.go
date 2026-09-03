package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/SabienNguyen/kancli/internal/board"
)

// doneColumns maps every board to its last column.
func DoneColumns(f *board.File) map[string]string {
	out := map[string]string{}
	for _, b := range f.Boards {
		if c := b.DoneColumn(); c != nil {
			out[b.ID] = c.ID
		}
	}
	return out
}

// duckdbBinary finds the DuckDB command-line shell, if installed.
func DuckDBBinary() (string, error) {
	if p := os.Getenv("KANCLI_DUCKDB"); p != "" {
		return p, nil
	}
	p, err := exec.LookPath("duckdb")
	if err != nil {
		return "", fmt.Errorf("the duckdb command-line tool is not installed (see https://duckdb.org/docs/installation)")
	}
	return p, nil
}

// sqlLiteral quotes a string for SQL.
func SQLLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// WriteEventsFile exports the event log to a temporary JSONL file so
// DuckDB can read it, and returns the path and a cleanup function. The
// path is empty with a no-op cleanup when the store has nothing to export.
func WriteEventsFile(s *Store) (string, func(), error) {
	if !s.Enabled() {
		return "", func() {}, nil
	}
	tmp, err := os.CreateTemp("", "kancli-events-*.jsonl")
	if err != nil {
		return "", nil, err
	}
	name := tmp.Name()
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return "", nil, err
	}
	if err := s.ExportEventsJSONL(name); err != nil {
		os.Remove(name)
		return "", nil, err
	}
	return name, func() { os.Remove(name) }, nil
}

// sqlViews returns SQL that defines the boards, columns, tasks, events and
// derived views over the given state file and event log files. doneColumns
// maps each board id to its last column, which defines "finished".
func SQLViews(stateFile string, eventFiles []string, DoneColumns map[string]string) string {
	var sb strings.Builder
	sb.WriteString("-- kancli views. Load with: duckdb -init kancli.sql\n")
	ids := make([]string, 0, len(DoneColumns))
	for id := range DoneColumns {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	rows := make([]string, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, fmt.Sprintf("(%s, %s)", SQLLiteral(id), SQLLiteral(DoneColumns[id])))
	}
	if len(rows) == 0 {
		rows = append(rows, "(NULL::VARCHAR, NULL::VARCHAR)")
	}
	fmt.Fprintf(&sb, "CREATE OR REPLACE VIEW done_columns AS SELECT * FROM (VALUES %s) t(board, id)%s;\n", strings.Join(rows, ", "), map[bool]string{true: " WHERE board IS NOT NULL", false: ""}[len(DoneColumns) == 0])
	fmt.Fprintf(&sb, "CREATE OR REPLACE VIEW boards AS SELECT b.id, b.name, b.description, len(b.tasks) AS tasks FROM (SELECT unnest(boards) AS b FROM read_json_auto(%s));\n", SQLLiteral(stateFile))
	fmt.Fprintf(&sb, "CREATE OR REPLACE VIEW columns AS SELECT b.id AS board, unnest(b.columns, recursive := true) FROM (SELECT unnest(boards) AS b FROM read_json_auto(%s));\n", SQLLiteral(stateFile))
	fmt.Fprintf(&sb, "CREATE OR REPLACE VIEW tasks AS SELECT b.id AS board, unnest(b.tasks, recursive := true) FROM (SELECT unnest(boards) AS b FROM read_json_auto(%s));\n", SQLLiteral(stateFile))
	if len(eventFiles) == 0 {
		sb.WriteString("CREATE OR REPLACE VIEW events AS SELECT NULL::BIGINT AS seq, NULL::TIMESTAMP AS at, NULL::VARCHAR AS board, NULL::VARCHAR AS kind, NULL::BIGINT AS task, NULL::VARCHAR AS \"from\", NULL::VARCHAR AS \"to\", NULL::BIGINT AS index, NULL::VARCHAR AS text, NULL::JSON AS data, NULL::VARCHAR AS actor WHERE false;\n")
	} else {
		quoted := make([]string, len(eventFiles))
		for i, f := range eventFiles {
			quoted[i] = SQLLiteral(f)
		}
		fmt.Fprintf(&sb, "CREATE OR REPLACE VIEW events AS SELECT * FROM read_json_auto([%s], format = 'newline_delimited', union_by_name = true) ORDER BY seq;\n", strings.Join(quoted, ", "))
	}
	sb.WriteString(`CREATE OR REPLACE VIEW moves AS
  SELECT seq, at, board, task, "from" AS from_column, "to" AS to_column FROM events WHERE kind = 'task.moved';
CREATE OR REPLACE VIEW column_stays AS
  SELECT board, task, to_column AS "column", at AS entered,
         lead(at) OVER (PARTITION BY board, task ORDER BY seq) AS left
  FROM moves;
CREATE OR REPLACE VIEW cycle_times AS
  SELECT c.board, c.task, c.at AS created_at, min(d.at) AS done_at, min(d.at) - c.at AS cycle
  FROM events c
  JOIN moves d USING (board, task)
  JOIN done_columns dc ON dc.board = c.board AND dc.id = d.to_column
  WHERE c.kind = 'task.created'
  GROUP BY ALL;
`)
	return sb.String()
}

// exampleQueries are shown by `kancli stats -sql`.
const ExampleQueries = `-- Throughput per ISO week
SELECT date_trunc('week', at) AS week, count(*) AS done
FROM moves WHERE to_column = 'done' GROUP BY ALL ORDER BY week;

-- Median cycle time per label
SELECT label, median(cycle) AS cycle
FROM cycle_times ct
JOIN tasks t ON t.board = ct.board AND t.id = ct.task,
     unnest(t.labels) AS l(label)
GROUP BY ALL ORDER BY cycle DESC;

-- Average hours spent in each column
SELECT "column", avg(date_diff('hour', entered, left)) AS hours
FROM column_stays WHERE left IS NOT NULL GROUP BY ALL;
`

// runDuckDB executes SQL through the DuckDB shell with the kancli views
// defined. format is box, json, csv or markdown.
func RunDuckDB(bin, views, query, format string) (string, error) {
	flag := map[string]string{"box": "-box", "json": "-json", "csv": "-csv", "markdown": "-markdown", "md": "-markdown", "table": "-table"}[format]
	if flag == "" {
		return "", fmt.Errorf("unknown format %q (use box, json, csv or markdown)", format)
	}
	cmd := exec.Command(bin, flag, "-batch")
	cmd.Stdin = strings.NewReader(views + "\n" + strings.TrimSpace(query) + "\n")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), fmt.Errorf("duckdb: %s", msg)
	}
	return out.String(), nil
}

// writeStateFile writes the current in-memory state to a temp JSON file so
// the tasks view is current even before the next snapshot.
func WriteStateFile(f *board.File) (string, func(), error) {
	tmp, err := os.CreateTemp("", "kancli-state-*.json")
	if err != nil {
		return "", nil, err
	}
	enc := json.NewEncoder(tmp)
	if err := enc.Encode(f); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", nil, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", nil, err
	}
	return tmp.Name(), func() { os.Remove(tmp.Name()) }, nil
}
