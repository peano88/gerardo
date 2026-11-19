package ui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/peano88/gerrit-tui/internal/gerrit"
)

var (
	accent        = lipgloss.Color("12")
	selectedBg    = lipgloss.AdaptiveColor{Light: "254", Dark: "236"}
	sectionStyle  = lipgloss.NewStyle().Bold(true).Foreground(accent)
	columnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Bold(true)
	numberStyle   = lipgloss.NewStyle().Foreground(accent)
	approvedStyle = addStyle.Bold(true)
	rejectedStyle = delStyle.Bold(true)
)

const (
	listGutter = 2
	cellGap    = "  "
)

type dashColumns struct {
	number, subject, owner, vote, updated int
}

func newDashColumns(width int, compact bool) dashColumns {
	c := dashColumns{number: 7, owner: 24, vote: 2, updated: 4}
	if compact {
		c.owner = 14
	}
	fixed := listGutter + c.number + c.owner + 2*c.vote + c.updated + 5*len(cellGap)
	c.subject = max(width-fixed, 10)
	return c
}

// changeIndexes returns the positions of the selectable items.
func (d *dashboard) changeIndexes() []int {
	var idx []int
	for i, it := range d.items {
		if it.change != nil {
			idx = append(idx, i)
		}
	}
	return idx
}

// move moves the cursor by delta changes, skipping section headers.
func (d *dashboard) move(delta int) {
	idx := d.changeIndexes()
	if len(idx) == 0 {
		d.cursor = 0
		return
	}
	k := 0
	for i, v := range idx {
		if v <= d.cursor {
			k = i
		}
	}
	d.cursor = idx[min(max(k+delta, 0), len(idx)-1)]
	d.scroll()
}

// selectNumber puts the cursor on change n, or on the first change.
func (d *dashboard) selectNumber(n int) {
	idx := d.changeIndexes()
	d.cursor = 0
	if len(idx) > 0 {
		d.cursor = idx[0]
	}
	for _, i := range idx {
		if d.items[i].change.Number == n {
			d.cursor = i
		}
	}
	d.scroll()
}

func (d *dashboard) listHeight() int {
	return max(d.height-1, 1)
}

// scroll keeps the cursor visible, with the header of its section when it
// is the first change of it.
func (d *dashboard) scroll() {
	h := d.listHeight()
	top := d.cursor
	if top > 0 && d.items[top-1].change == nil {
		top--
	}
	if top < d.offset {
		d.offset = top
	}
	if d.cursor >= d.offset+h {
		d.offset = d.cursor - h + 1
	}
	d.offset = min(max(d.offset, 0), max(len(d.items)-h, 0))
}

func (d *dashboard) listView(width int) string {
	cols := newDashColumns(width, d.split())
	lines := []string{d.columnHeader(cols)}
	if len(d.items) == 0 && !d.loading {
		lines = append(lines, strings.Repeat(" ", listGutter)+dimStyle.Render("No changes match "+d.query))
	}
	for i := d.offset; i < min(len(d.items), d.offset+d.listHeight()); i++ {
		if d.items[i].change == nil {
			lines = append(lines, sectionLine(d.items[i].header, width))
		} else {
			lines = append(lines, changeLine(d.items[i].change, cols, i == d.cursor))
		}
	}
	return lipgloss.NewStyle().Width(width).Height(d.height).Render(strings.Join(lines, "\n"))
}

func (d *dashboard) columnHeader(c dashColumns) string {
	cell := func(s string, w int, right bool) string {
		return renderCell(columnStyle, lipgloss.NewStyle(), s, w, right)
	}
	return strings.Repeat(" ", listGutter) + strings.Join([]string{
		cell("#", c.number, true),
		cell("Subject", c.subject, false),
		cell("Owner", c.owner, false),
		cell("CR", c.vote, false),
		cell("V", c.vote, false),
		cell("Upd", c.updated, true),
	}, cellGap)
}

func sectionLine(title string, width int) string {
	rule := max(width-listGutter-ansi.StringWidth(title)-1, 0)
	return strings.Repeat(" ", listGutter) + sectionStyle.Render(title) + " " + dimStyle.Render(strings.Repeat("─", rule))
}

func changeLine(c *gerrit.ChangeInfo, cols dashColumns, selected bool) string {
	base := lipgloss.NewStyle()
	gutter := "  "
	if selected {
		base = base.Background(selectedBg)
		gutter = lipgloss.NewStyle().Foreground(accent).Inherit(base).Render("▌ ")
	}

	subject, subjectStyle := c.Subject, lipgloss.NewStyle()
	if c.WorkInProgress {
		subject, subjectStyle = "[WIP] "+subject, dimStyle
	}
	if selected {
		subjectStyle = subjectStyle.Bold(true)
	}
	cr, crStyle := voteCell(c.Labels["Code-Review"])
	v, vStyle := voteCell(c.Labels["Verified"])

	gap := base.Render(cellGap)
	return gutter + strings.Join([]string{
		renderCell(numberStyle, base, strconv.Itoa(c.Number), cols.number, true),
		renderCell(subjectStyle, base, subject, cols.subject, false),
		renderCell(lipgloss.NewStyle(), base, c.Owner.DisplayName(), cols.owner, false),
		renderCell(crStyle, base, cr, cols.vote, false),
		renderCell(vStyle, base, v, cols.vote, false),
		renderCell(dimStyle, base, relativeTime(c.Updated.Time), cols.updated, true),
	}, gap)
}

// renderCell truncates s to w columns and pads it, on the row's background.
func renderCell(st, row lipgloss.Style, s string, w int, right bool) string {
	st = st.Inherit(row).Width(w)
	if right {
		st = st.Align(lipgloss.Right)
	}
	return st.Render(ansi.Truncate(s, w, "…"))
}

// voteCell shows the decisive vote of a label: ✓ approved, ✗ rejected.
func voteCell(l gerrit.LabelInfo) (string, lipgloss.Style) {
	switch {
	case l.Rejected != nil:
		return "✗", rejectedStyle
	case l.Approved != nil:
		return "✓", approvedStyle
	case l.Disliked != nil:
		return "-1", delStyle
	case l.Recommended != nil:
		return "+1", addStyle
	default:
		return "", dimStyle
	}
}
