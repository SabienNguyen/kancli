package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// duckdbBinary finds the DuckDB command-line shell, if installed.
func duckdbBinary() (string, error) {
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
func sqlLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// eventFiles lists every event log file, oldest segment first.
func (s *store) eventFiles() []string {
	if !s.enabled() {
		return nil
	}
	segs, _ := filepath.Glob(filepath.Join(s.archiveDir, "*.jsonl"))
	if exists(s.logPath) {
		segs = append(segs, s.logPath)
	}
	return segs
}

// sqlViews returns SQL that defines the boards, columns, tasks and events
// views over the given state file and event log files.
func sqlViews(stateFile string, eventFiles []string) string {
	var sb strings.Builder
	sb.WriteString("-- kancli views. Load with: duckdb -init kancli.sql\n")
	fmt.Fprintf(&sb, "CREATE OR REPLACE VIEW boards AS SELECT b.id, b.name, len(b.tasks) AS tasks FROM (SELECT unnest(boards) AS b FROM read_json_auto(%s));\n", sqlLiteral(stateFile))
	fmt.Fprintf(&sb, "CREATE OR REPLACE VIEW columns AS SELECT b.id AS board, unnest(b.columns, recursive := true) FROM (SELECT unnest(boards) AS b FROM read_json_auto(%s));\n", sqlLiteral(stateFile))
	fmt.Fprintf(&sb, "CREATE OR REPLACE VIEW tasks AS SELECT b.id AS board, unnest(b.tasks, recursive := true) FROM (SELECT unnest(boards) AS b FROM read_json_auto(%s));\n", sqlLiteral(stateFile))
	if len(eventFiles) == 0 {
		sb.WriteString("CREATE OR REPLACE VIEW events AS SELECT NULL::BIGINT AS seq, NULL::TIMESTAMP AS at, NULL::VARCHAR AS board, NULL::VARCHAR AS kind, NULL::BIGINT AS task, NULL::VARCHAR AS \"from\", NULL::VARCHAR AS \"to\", NULL::BIGINT AS index, NULL::VARCHAR AS text, NULL::JSON AS data, NULL::VARCHAR AS actor WHERE false;\n")
	} else {
		quoted := make([]string, len(eventFiles))
		for i, f := range eventFiles {
			quoted[i] = sqlLiteral(f)
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
  SELECT c.board, c.task, c.at AS created_at, d.at AS done_at, d.at - c.at AS cycle
  FROM events c JOIN moves d USING (board, task)
  WHERE c.kind = 'task.created'
    AND d.to_column = (SELECT id FROM columns WHERE columns.board = c.board ORDER BY rowid DESC LIMIT 1);
`)
	return sb.String()
}

// exampleQueries are shown by `kancli stats -sql`.
const exampleQueries = `-- Throughput per ISO week
SELECT date_trunc('week', at) AS week, count(*) AS done
FROM moves WHERE to_column = 'done' GROUP BY ALL ORDER BY week;

-- Median cycle time per label
SELECT label, median(cycle) AS cycle
FROM cycle_times JOIN tasks USING (board, task) , unnest(tasks.labels) AS l(label)
GROUP BY ALL ORDER BY cycle DESC;

-- Average hours spent in each column
SELECT "column", avg(date_diff('hour', entered, left)) AS hours
FROM column_stays WHERE left IS NOT NULL GROUP BY ALL;
`

// runDuckDB executes SQL through the DuckDB shell with the kancli views
// defined. format is box, json, csv or markdown.
func runDuckDB(bin, views, query, format string) (string, error) {
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
func writeStateFile(f *File) (string, func(), error) {
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
