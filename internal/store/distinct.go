package store

import "fmt"

// skipScanDistinctSQL lists the distinct non-NULL values of column in ascending
// order. SELECT DISTINCT reads every row (or index entry) even when only a few
// values exist; on the large per-snapshot value tables that is 100-200ms per
// call. This recursive form seeks from one value to the next through an index
// on column, so its cost tracks the number of distinct values instead.
//
// table and column must be compile-time constants: identifiers cannot be bound
// as parameters.
func skipScanDistinctSQL(table, column string) string {
	return fmt.Sprintf(`WITH RECURSIVE d(v) AS (
		SELECT MIN(%[2]s) FROM %[1]s
		UNION ALL
		SELECT (SELECT MIN(%[2]s) FROM %[1]s WHERE %[2]s > d.v) FROM d WHERE d.v IS NOT NULL
	) SELECT v FROM d WHERE v IS NOT NULL`, table, column)
}
