package importer

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"unicode/utf8"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/money"
)

// NewCSV parses a UTF-8 CSV file (comma, semicolon or tab separated, as
// spreadsheet apps export them in different locales; an optional BOM is
// ignored).
func NewCSV(data []byte, cur money.Currency) (*Tabular, error) {
	if len(data) > domain.MaxImportFileBytes {
		return nil, sourceErr("file_too_large", "at most %d MiB", domain.MaxImportFileBytes>>20)
	}
	data = bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
	if !utf8.Valid(data) {
		return nil, sourceErr("not_utf8", "save the file as CSV UTF-8")
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = delimiter(data)
	r.FieldsPerRecord = -1
	var rows [][]string
	var lines []int
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				return nil, sourceErr("csv_malformed", "line %d: %v", pe.Line, pe.Err)
			}
			return nil, sourceErr("csv_malformed", "%v", err)
		}
		if len(rows) > maxRows {
			return nil, sourceErr("too_many_rows", "at most %d rows per file", maxRows)
		}
		line, _ := r.FieldPos(0)
		rows, lines = append(rows, rec), append(lines, line)
	}
	if len(rows) == 0 {
		return nil, sourceErr("empty_file", "the file has no header row")
	}
	return newTabular(rows, lines, cur)
}

// delimiter picks the separator that appears most in the header line.
func delimiter(data []byte) rune {
	line := data
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		line = data[:i]
	}
	best, n := ',', bytes.Count(line, []byte{','})
	for _, d := range []byte{';', '\t'} {
		if c := bytes.Count(line, []byte{d}); c > n {
			best, n = rune(d), c
		}
	}
	return best
}
