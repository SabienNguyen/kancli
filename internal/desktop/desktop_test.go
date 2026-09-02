package desktop

import (
	"testing"
)

func TestPSString(t *testing.T) {
	if got := psString(`Fix $(Remove-Item x) it's "bad"`); got != `'Fix $(Remove-Item x) it''s "bad"'` {
		t.Errorf("psString = %s", got)
	}
}
