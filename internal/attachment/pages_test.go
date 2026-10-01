package attachment

import "testing"

func TestPageCount_CountsPageObjectsNotTheTree(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		want int
	}{
		{"two pages and the tree", "%PDF-1.4\n1 0 obj <</Type /Pages /Count 2>>\n2 0 obj <</Type /Page>>\n3 0 obj <</Type/Page /Parent 1 0 R>>\n", 2},
		{"compressed objects", "%PDF-1.7\nstream x\x9c\x01\x02 endstream", 0},
		{"not a pdf", "hello", 0},
	} {
		if got := PageCount([]byte(tc.data)); got != tc.want {
			t.Errorf("%s: PageCount = %d, want %d", tc.name, got, tc.want)
		}
	}
}
