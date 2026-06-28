package types

import (
	"testing"
	"time"
)

func templateNames(templates []*VMTemplate) []string {
	out := make([]string, len(templates))
	for i, t := range templates {
		out[i] = t.Name
	}
	return out
}

func TestSortTemplates_ByID_Asc(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-3", Name: "c"},
		{ID: "tpl-1", Name: "a"},
		{ID: "tpl-2", Name: "b"},
	}
	SortTemplates(templates, TemplateSortID, SortOrderAsc)
	want := []string{"tpl-1", "tpl-2", "tpl-3"}
	for i, tpl := range templates {
		if tpl.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q", i, tpl.ID, want[i])
		}
	}
}

func TestSortTemplates_ByName_CaseInsensitive(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-3", Name: "Charlie"},
		{ID: "tpl-1", Name: "alpha"},
		{ID: "tpl-2", Name: "Bravo"},
	}
	SortTemplates(templates, TemplateSortName, SortOrderAsc)
	got := templateNames(templates)
	want := []string{"alpha", "Bravo", "Charlie"}
	for i, n := range got {
		if n != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %v)", i, n, want[i], got)
		}
	}
}

func TestSortTemplates_ByCreatedAt_DescTiebreaksOnID(t *testing.T) {
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	templates := []*VMTemplate{
		{ID: "tpl-2", Name: "b", CreatedAt: t0},
		{ID: "tpl-1", Name: "a", CreatedAt: t0},
		{ID: "tpl-3", Name: "c", CreatedAt: t0.Add(time.Hour)},
	}
	SortTemplates(templates, TemplateSortCreatedAt, SortOrderDesc)
	// Newest first; equal-time pair reverses on tiebreak when descending.
	got := templateNames(templates)
	want := []string{"c", "b", "a"}
	for i, n := range got {
		if n != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %v)", i, n, want[i], got)
		}
	}
}

func TestSortTemplates_ByCreatedAt_AscTiebreaksOnID(t *testing.T) {
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	templates := []*VMTemplate{
		{ID: "tpl-3", Name: "c", CreatedAt: t0.Add(time.Hour)},
		{ID: "tpl-2", Name: "b", CreatedAt: t0},
		{ID: "tpl-1", Name: "a", CreatedAt: t0},
	}
	SortTemplates(templates, TemplateSortCreatedAt, SortOrderAsc)
	if templates[0].ID != "tpl-1" || templates[1].ID != "tpl-2" || templates[2].ID != "tpl-3" {
		t.Errorf("got %q,%q,%q want tpl-1,tpl-2,tpl-3",
			templates[0].ID, templates[1].ID, templates[2].ID)
	}
}

func TestSortTemplates_UnknownFieldFallsBackToID(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-3"},
		{ID: "tpl-1"},
		{ID: "tpl-2"},
	}
	// `ram_mb` is now a valid template sort axis (5.4.57); use a sentinel
	// that's still unsupported so the fallback path is exercised.
	SortTemplates(templates, "memory", SortOrderAsc)
	want := []string{"tpl-1", "tpl-2", "tpl-3"}
	for i, tpl := range templates {
		if tpl.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q", i, tpl.ID, want[i])
		}
	}
}

func TestSortTemplates_ByCPUs_AscTiebreaksOnID(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-2", Name: "b", CPUs: 4},
		{ID: "tpl-1", Name: "a", CPUs: 4}, // tie with tpl-2
		{ID: "tpl-3", Name: "c", CPUs: 1},
		{ID: "tpl-4", Name: "d", CPUs: 8},
	}
	SortTemplates(templates, TemplateSortCPUs, SortOrderAsc)
	got := []string{templates[0].ID, templates[1].ID, templates[2].ID, templates[3].ID}
	want := []string{"tpl-3", "tpl-1", "tpl-2", "tpl-4"} // 1 < 4(tpl-1<tpl-2) < 8
	for i, id := range got {
		if id != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, id, want[i], got)
		}
	}
}

func TestSortTemplates_ByCPUs_Desc(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-1", Name: "a", CPUs: 1},
		{ID: "tpl-2", Name: "b", CPUs: 4},
		{ID: "tpl-3", Name: "c", CPUs: 8},
	}
	SortTemplates(templates, TemplateSortCPUs, SortOrderDesc)
	got := []string{templates[0].ID, templates[1].ID, templates[2].ID}
	want := []string{"tpl-3", "tpl-2", "tpl-1"}
	for i, id := range got {
		if id != want[i] {
			t.Errorf("idx %d: id = %q, want %q", i, id, want[i])
		}
	}
}

func TestSortTemplates_ByRAMMB_AscTiebreaksOnID(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-2", Name: "b", RAMMB: 2048},
		{ID: "tpl-1", Name: "a", RAMMB: 2048}, // tie with tpl-2
		{ID: "tpl-3", Name: "c", RAMMB: 1024},
	}
	SortTemplates(templates, TemplateSortRAMMB, SortOrderAsc)
	if templates[0].ID != "tpl-3" || templates[1].ID != "tpl-1" || templates[2].ID != "tpl-2" {
		t.Errorf("ram_mb asc with tie wrong: got %q,%q,%q want tpl-3,tpl-1,tpl-2", templates[0].ID, templates[1].ID, templates[2].ID)
	}
}

func TestSortTemplates_ByDiskGB_DescTiebreaksOnID(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-1", Name: "a", DiskGB: 100},
		{ID: "tpl-2", Name: "b", DiskGB: 100}, // tie with tpl-1
		{ID: "tpl-3", Name: "c", DiskGB: 20},
	}
	SortTemplates(templates, TemplateSortDiskGB, SortOrderDesc)
	// Largest disk first; equal-disk pair reverses on tiebreak too because
	// the descending wrapper inverts the entire compare result.
	if templates[0].ID != "tpl-2" || templates[1].ID != "tpl-1" || templates[2].ID != "tpl-3" {
		t.Errorf("disk_gb desc with tie wrong: got %q,%q,%q want tpl-2,tpl-1,tpl-3", templates[0].ID, templates[1].ID, templates[2].ID)
	}
}

func TestSortTemplates_ByImage_AscEmptyTrailing(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-3", Name: "c", Image: ""},
		{ID: "tpl-1", Name: "a", Image: "ubuntu-22.04.qcow2"},
		{ID: "tpl-2", Name: "b", Image: "rocky9.qcow2"},
	}
	SortTemplates(templates, TemplateSortImage, SortOrderAsc)
	// rocky9 < ubuntu-22.04 lex; empty trails in asc.
	want := []string{"tpl-2", "tpl-1", "tpl-3"}
	for i, tpl := range templates {
		if tpl.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, tpl.ID, want[i], templates)
		}
	}
}

func TestSortTemplates_ByImage_DescEmptyLeading(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-1", Name: "a", Image: "ubuntu-22.04.qcow2"},
		{ID: "tpl-3", Name: "c", Image: ""},
		{ID: "tpl-2", Name: "b", Image: "rocky9.qcow2"},
	}
	SortTemplates(templates, TemplateSortImage, SortOrderDesc)
	// Desc inverts the asc result: empty leads, then ubuntu, then rocky.
	want := []string{"tpl-3", "tpl-1", "tpl-2"}
	for i, tpl := range templates {
		if tpl.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, tpl.ID, want[i], templates)
		}
	}
}

func TestSortTemplates_ByImage_CaseInsensitive(t *testing.T) {
	// Operators paste base-image names verbatim from the images directory and
	// case shouldn't split the cohort.
	templates := []*VMTemplate{
		{ID: "tpl-3", Name: "c", Image: "Rocky9.qcow2"},
		{ID: "tpl-1", Name: "a", Image: "rocky9.qcow2"}, // same as tpl-3 case-folded
		{ID: "tpl-2", Name: "b", Image: "alpine.qcow2"},
	}
	SortTemplates(templates, TemplateSortImage, SortOrderAsc)
	// alpine < rocky9 < rocky9 (tie tiebroken on id: tpl-1 < tpl-3).
	want := []string{"tpl-2", "tpl-1", "tpl-3"}
	for i, tpl := range templates {
		if tpl.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, tpl.ID, want[i], templates)
		}
	}
}

func TestSortTemplates_ByImage_TiebreaksOnID(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-3", Name: "c", Image: "rocky9.qcow2"},
		{ID: "tpl-1", Name: "a", Image: "rocky9.qcow2"},
		{ID: "tpl-2", Name: "b", Image: "rocky9.qcow2"},
	}
	SortTemplates(templates, TemplateSortImage, SortOrderAsc)
	if templates[0].ID != "tpl-1" || templates[1].ID != "tpl-2" || templates[2].ID != "tpl-3" {
		t.Errorf("got %q,%q,%q want tpl-1,tpl-2,tpl-3",
			templates[0].ID, templates[1].ID, templates[2].ID)
	}
}

func TestSortTemplates_ByImage_AllEmpty_TiebreaksOnID(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-3"},
		{ID: "tpl-1"},
		{ID: "tpl-2"},
	}
	SortTemplates(templates, TemplateSortImage, SortOrderAsc)
	if templates[0].ID != "tpl-1" || templates[1].ID != "tpl-2" || templates[2].ID != "tpl-3" {
		t.Errorf("got %q,%q,%q want tpl-1,tpl-2,tpl-3 — empty/empty pair must tiebreak on id",
			templates[0].ID, templates[1].ID, templates[2].ID)
	}
}

func TestIsValidTemplateSort_AcceptsImage(t *testing.T) {
	cases := []struct {
		field string
		want  bool
	}{
		{TemplateSortID, true},
		{TemplateSortName, true},
		{TemplateSortCreatedAt, true},
		{TemplateSortCPUs, true},
		{TemplateSortRAMMB, true},
		{TemplateSortDiskGB, true},
		{TemplateSortImage, true},
		{TemplateSortDefaultUser, true},
		{TemplateSortOSType, true},
		{TemplateSortOSVariant, true},
		{TemplateSortDescription, true},
		{"bogus", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsValidTemplateSort(tc.field); got != tc.want {
			t.Errorf("IsValidTemplateSort(%q) = %v, want %v", tc.field, got, tc.want)
		}
	}
}

// 5.4.102 — case-insensitive `os_type` sort axis on the template list.
// Diverges from the nil-trailing convention because empty stored OSType
// resolves to "linux" via VMTemplate.ResolvedOSType.

func TestSortTemplates_ByOSType_AscEmptyResolvesToLinux(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-3", Name: "c", OSType: OSTypeWindows},
		{ID: "tpl-1", Name: "a"},                  // empty → resolves to linux
		{ID: "tpl-2", Name: "b", OSType: "linux"}, // explicit linux
	}
	SortTemplates(templates, TemplateSortOSType, SortOrderAsc)
	// linux < windows. tpl-1 (empty→linux) and tpl-2 (explicit linux)
	// interleave by id tiebreak; tpl-3 (windows) trails.
	want := []string{"tpl-1", "tpl-2", "tpl-3"}
	for i, tpl := range templates {
		if tpl.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, tpl.ID, want[i], templates)
		}
	}
}

func TestSortTemplates_ByOSType_DescEmptyResolvesToLinux(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-3", Name: "c", OSType: OSTypeWindows},
		{ID: "tpl-1", Name: "a"},                  // empty → linux
		{ID: "tpl-2", Name: "b", OSType: "linux"}, // explicit linux
	}
	SortTemplates(templates, TemplateSortOSType, SortOrderDesc)
	// Desc: windows heads, then linux pair reversed by id tiebreak.
	want := []string{"tpl-3", "tpl-2", "tpl-1"}
	for i, tpl := range templates {
		if tpl.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, tpl.ID, want[i], templates)
		}
	}
}

func TestSortTemplates_ByOSType_CaseInsensitive(t *testing.T) {
	// Mirrors the case-insensitive `?os_type=` filter contract — case
	// shouldn't split the windows cohort regardless of how it was stored.
	templates := []*VMTemplate{
		{ID: "tpl-3", Name: "c", OSType: "WINDOWS"},
		{ID: "tpl-1", Name: "a", OSType: "windows"},
		{ID: "tpl-2", Name: "b", OSType: "Linux"},
	}
	SortTemplates(templates, TemplateSortOSType, SortOrderAsc)
	// linux < windows; the two windows entries tiebreak on id.
	want := []string{"tpl-2", "tpl-1", "tpl-3"}
	for i, tpl := range templates {
		if tpl.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, tpl.ID, want[i], templates)
		}
	}
}

func TestSortTemplates_ByOSType_TiebreaksOnID(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-3", OSType: OSTypeWindows},
		{ID: "tpl-1", OSType: OSTypeWindows},
		{ID: "tpl-2", OSType: OSTypeWindows},
	}
	SortTemplates(templates, TemplateSortOSType, SortOrderAsc)
	if templates[0].ID != "tpl-1" || templates[1].ID != "tpl-2" || templates[2].ID != "tpl-3" {
		t.Errorf("got %q,%q,%q want tpl-1,tpl-2,tpl-3",
			templates[0].ID, templates[1].ID, templates[2].ID)
	}
}

func TestSortTemplates_ByOSType_AllEmpty_TiebreaksOnID(t *testing.T) {
	// All empty → all resolve to linux, so id tiebreak determines order.
	templates := []*VMTemplate{
		{ID: "tpl-3"},
		{ID: "tpl-1"},
		{ID: "tpl-2"},
	}
	SortTemplates(templates, TemplateSortOSType, SortOrderAsc)
	if templates[0].ID != "tpl-1" || templates[1].ID != "tpl-2" || templates[2].ID != "tpl-3" {
		t.Errorf("got %q,%q,%q want tpl-1,tpl-2,tpl-3 — all-empty must tiebreak on id",
			templates[0].ID, templates[1].ID, templates[2].ID)
	}
}

// 5.4.92 — case-insensitive `default_user` sort axis on the template list.
// Diverges from the VM list `default_user` axis (5.4.91): empty stored values
// sink to the tail of asc / head of desc rather than collapsing to "root",
// because templates store empty as "use the image's built-in user".

func TestSortTemplates_ByDefaultUser_AscEmptyTrailing(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-3", Name: "c", DefaultUser: ""},
		{ID: "tpl-1", Name: "a", DefaultUser: "ubuntu"},
		{ID: "tpl-2", Name: "b", DefaultUser: "ops-alice"},
	}
	SortTemplates(templates, TemplateSortDefaultUser, SortOrderAsc)
	// ops-alice < ubuntu lex; empty trails in asc (unlike VM list, where
	// empty would collapse to root and collate alphabetically).
	want := []string{"tpl-2", "tpl-1", "tpl-3"}
	for i, tpl := range templates {
		if tpl.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, tpl.ID, want[i], templates)
		}
	}
}

func TestSortTemplates_ByDefaultUser_DescEmptyLeading(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-1", Name: "a", DefaultUser: "ubuntu"},
		{ID: "tpl-3", Name: "c", DefaultUser: ""},
		{ID: "tpl-2", Name: "b", DefaultUser: "ops-alice"},
	}
	SortTemplates(templates, TemplateSortDefaultUser, SortOrderDesc)
	// Desc inverts: empty leads, then ubuntu, then ops-alice.
	want := []string{"tpl-3", "tpl-1", "tpl-2"}
	for i, tpl := range templates {
		if tpl.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, tpl.ID, want[i], templates)
		}
	}
}

func TestSortTemplates_ByDefaultUser_CaseInsensitive(t *testing.T) {
	// Mirrors the case-insensitive `?default_user=` filter contract — operators
	// paste user names verbatim and case shouldn't split the cohort.
	templates := []*VMTemplate{
		{ID: "tpl-3", Name: "c", DefaultUser: "Root"},
		{ID: "tpl-1", Name: "a", DefaultUser: "root"}, // case-folded same
		{ID: "tpl-2", Name: "b", DefaultUser: "alice"},
	}
	SortTemplates(templates, TemplateSortDefaultUser, SortOrderAsc)
	// alice < root (case-folded); root tie tiebreaks on id (tpl-1 < tpl-3).
	want := []string{"tpl-2", "tpl-1", "tpl-3"}
	for i, tpl := range templates {
		if tpl.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, tpl.ID, want[i], templates)
		}
	}
}

func TestSortTemplates_ByDefaultUser_EmptyDoesNotCollateWithRoot(t *testing.T) {
	// Unlike VM list `default_user` (5.4.91) — which collapses empty → "root"
	// to mirror runtime semantics — templates store empty as "use the image's
	// built-in user" so an empty stored value must sink to the tail of asc
	// rather than interleave with explicit "root" rows.
	templates := []*VMTemplate{
		{ID: "tpl-3", Name: "c", DefaultUser: ""},
		{ID: "tpl-1", Name: "a", DefaultUser: "root"},
		{ID: "tpl-2", Name: "b", DefaultUser: "root"},
	}
	SortTemplates(templates, TemplateSortDefaultUser, SortOrderAsc)
	want := []string{"tpl-1", "tpl-2", "tpl-3"} // root, root, then empty trails
	for i, tpl := range templates {
		if tpl.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, tpl.ID, want[i], templates)
		}
	}
}

func TestSortTemplates_ByDefaultUser_TiebreaksOnID(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-3", Name: "c", DefaultUser: "ubuntu"},
		{ID: "tpl-1", Name: "a", DefaultUser: "ubuntu"},
		{ID: "tpl-2", Name: "b", DefaultUser: "ubuntu"},
	}
	SortTemplates(templates, TemplateSortDefaultUser, SortOrderAsc)
	if templates[0].ID != "tpl-1" || templates[1].ID != "tpl-2" || templates[2].ID != "tpl-3" {
		t.Errorf("got %q,%q,%q want tpl-1,tpl-2,tpl-3",
			templates[0].ID, templates[1].ID, templates[2].ID)
	}
}

func TestSortTemplates_ByDefaultUser_AllEmpty_TiebreaksOnID(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-3"},
		{ID: "tpl-1"},
		{ID: "tpl-2"},
	}
	SortTemplates(templates, TemplateSortDefaultUser, SortOrderAsc)
	if templates[0].ID != "tpl-1" || templates[1].ID != "tpl-2" || templates[2].ID != "tpl-3" {
		t.Errorf("got %q,%q,%q want tpl-1,tpl-2,tpl-3 — empty/empty pair must tiebreak on id",
			templates[0].ID, templates[1].ID, templates[2].ID)
	}
}

func TestSortTemplates_StablePagination(t *testing.T) {
	// Two independent SortTemplates invocations on the same equal-name
	// input must agree so page-1 + page-2 fetches see a deterministic
	// total ordering. The id tiebreak is what guarantees this — without
	// it, sort.SliceStable falls back to original slice order, which
	// shuffles between Go map iteration runs.
	build := func() []*VMTemplate {
		return []*VMTemplate{
			{ID: "tpl-3", Name: "shared"},
			{ID: "tpl-1", Name: "shared"},
			{ID: "tpl-4", Name: "shared"},
			{ID: "tpl-2", Name: "shared"},
		}
	}
	a := build()
	b := build()
	SortTemplates(a, TemplateSortName, SortOrderAsc)
	SortTemplates(b, TemplateSortName, SortOrderAsc)
	for i := range a {
		if a[i].ID != b[i].ID {
			t.Fatalf("idx %d: a=%q b=%q — equal-name tie not deterministic", i, a[i].ID, b[i].ID)
		}
	}
}

// 5.4.115 — case-insensitive `os_variant` sort axis on the template list.
// Mirrors the VM list `os_variant` sort axis (5.4.103) — empty stored values
// sink to the tail of asc / head of desc (no documented default unlike
// `os_type` which collapses empty → linux).

func TestSortTemplates_ByOSVariant_AscNilTrailing(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-1", OSType: OSTypeWindows, OSVariant: "windows-server-2025"},
		{ID: "tpl-2", OSType: OSTypeWindows, OSVariant: "windows-10"},
		{ID: "tpl-3"}, // empty → trails in asc
		{ID: "tpl-4", OSType: OSTypeWindows, OSVariant: "windows-11"},
	}
	SortTemplates(templates, TemplateSortOSVariant, SortOrderAsc)
	// Asc alphabetical: windows-10 < windows-11 < windows-server-2025; empty trails.
	want := []string{"tpl-2", "tpl-4", "tpl-1", "tpl-3"}
	for i, tpl := range templates {
		if tpl.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, tpl.ID, want[i], templates)
		}
	}
}

func TestSortTemplates_ByOSVariant_DescNilLeading(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-1", OSType: OSTypeWindows, OSVariant: "windows-server-2025"},
		{ID: "tpl-2", OSType: OSTypeWindows, OSVariant: "windows-10"},
		{ID: "tpl-3"}, // empty → leads in desc
		{ID: "tpl-4", OSType: OSTypeWindows, OSVariant: "windows-11"},
	}
	SortTemplates(templates, TemplateSortOSVariant, SortOrderDesc)
	// Desc: empty heads, then windows-server-2025 > windows-11 > windows-10.
	want := []string{"tpl-3", "tpl-1", "tpl-4", "tpl-2"}
	for i, tpl := range templates {
		if tpl.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, tpl.ID, want[i], templates)
		}
	}
}

func TestSortTemplates_ByOSVariant_CaseInsensitive(t *testing.T) {
	// Mirrors the case-insensitive `?os_variant=` filter contract — case
	// shouldn't split the windows-11 cohort regardless of how it was stored.
	templates := []*VMTemplate{
		{ID: "tpl-3", OSType: OSTypeWindows, OSVariant: "Windows-11"},
		{ID: "tpl-1", OSType: OSTypeWindows, OSVariant: "WINDOWS-11"},
		{ID: "tpl-2", OSType: OSTypeWindows, OSVariant: "windows-10"},
	}
	SortTemplates(templates, TemplateSortOSVariant, SortOrderAsc)
	// windows-10 < windows-11; the two windows-11 entries tiebreak on id.
	want := []string{"tpl-2", "tpl-1", "tpl-3"}
	for i, tpl := range templates {
		if tpl.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, tpl.ID, want[i], templates)
		}
	}
}

func TestSortTemplates_ByOSVariant_TiebreaksOnID(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-3", OSType: OSTypeWindows, OSVariant: "windows-11"},
		{ID: "tpl-1", OSType: OSTypeWindows, OSVariant: "windows-11"},
		{ID: "tpl-2", OSType: OSTypeWindows, OSVariant: "windows-11"},
	}
	SortTemplates(templates, TemplateSortOSVariant, SortOrderAsc)
	if templates[0].ID != "tpl-1" || templates[1].ID != "tpl-2" || templates[2].ID != "tpl-3" {
		t.Errorf("got %q,%q,%q want tpl-1,tpl-2,tpl-3",
			templates[0].ID, templates[1].ID, templates[2].ID)
	}
}

func TestSortTemplates_ByOSVariant_AllEmpty_TiebreaksOnID(t *testing.T) {
	// All empty → all sink (no documented default), so id tiebreak determines order.
	templates := []*VMTemplate{
		{ID: "tpl-3"},
		{ID: "tpl-1"},
		{ID: "tpl-2"},
	}
	SortTemplates(templates, TemplateSortOSVariant, SortOrderAsc)
	if templates[0].ID != "tpl-1" || templates[1].ID != "tpl-2" || templates[2].ID != "tpl-3" {
		t.Errorf("got %q,%q,%q want tpl-1,tpl-2,tpl-3 — all-empty must tiebreak on id",
			templates[0].ID, templates[1].ID, templates[2].ID)
	}
}

// 5.4.119 — case-insensitive `description` sort axis on the template list.
// Mirrors the image list `description` axis (5.4.118) one resource over —
// empty stored values sink to the tail of asc / head of desc (no documented
// default for description because the field is genuinely "operator did not
// bother to write one").

func TestSortTemplates_ByDescription_AscCaseInsensitive(t *testing.T) {
	// Operators paste descriptions verbatim and case shouldn't split the cohort.
	templates := []*VMTemplate{
		{ID: "tpl-3", Description: "Charlie cluster template"},
		{ID: "tpl-1", Description: "alpha bootstrap template"},
		{ID: "tpl-2", Description: "Bravo prod template"},
	}
	SortTemplates(templates, TemplateSortDescription, SortOrderAsc)
	want := []string{"tpl-1", "tpl-2", "tpl-3"} // alpha < bravo < charlie
	for i, tpl := range templates {
		if tpl.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q", i, tpl.ID, want[i])
		}
	}
}

func TestSortTemplates_ByDescription_EmptyTrailsInAsc(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-3", Description: ""},
		{ID: "tpl-1", Description: "rocky base"},
		{ID: "tpl-2", Description: ""},
	}
	SortTemplates(templates, TemplateSortDescription, SortOrderAsc)
	want := []string{"tpl-1", "tpl-2", "tpl-3"} // rocky base first; the two empties trail in id-asc.
	for i, tpl := range templates {
		if tpl.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, tpl.ID, want[i], templates)
		}
	}
}

func TestSortTemplates_ByDescription_EmptyHeadsInDesc(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-1", Description: "rocky base"},
		{ID: "tpl-3", Description: ""},
		{ID: "tpl-2", Description: ""},
	}
	SortTemplates(templates, TemplateSortDescription, SortOrderDesc)
	want := []string{"tpl-3", "tpl-2", "tpl-1"} // empties head in desc (id-desc among empties), then rocky.
	for i, tpl := range templates {
		if tpl.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, tpl.ID, want[i], templates)
		}
	}
}

func TestSortTemplates_ByDescription_TiebreaksOnID(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-3", Description: "shared description"},
		{ID: "tpl-1", Description: "shared description"},
		{ID: "tpl-2", Description: "shared description"},
	}
	SortTemplates(templates, TemplateSortDescription, SortOrderAsc)
	if templates[0].ID != "tpl-1" || templates[1].ID != "tpl-2" || templates[2].ID != "tpl-3" {
		t.Errorf("got %q,%q,%q want tpl-1,tpl-2,tpl-3 — equal descriptions must tiebreak on id",
			templates[0].ID, templates[1].ID, templates[2].ID)
	}
}
