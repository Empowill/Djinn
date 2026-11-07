package querytext

import (
	"strconv"
	"strings"
	"time"

	queryv1 "github.com/empowill/djinn/gen/go/query/v1"
)

// The texts of typed messages, as the command line types them: what Filter, Order, Group and Measure read back.

var opTexts = map[queryv1.CompareOp]string{
	queryv1.CompareOp_COMPARE_OP_UNSPECIFIED: "=", queryv1.CompareOp_COMPARE_OP_EQUAL: "=",
	queryv1.CompareOp_COMPARE_OP_GREATER_THAN: ">", queryv1.CompareOp_COMPARE_OP_GREATER_THAN_EQUAL: ">=",
	queryv1.CompareOp_COMPARE_OP_LESS_THAN: "<", queryv1.CompareOp_COMPARE_OP_LESS_THAN_EQUAL: "<=",
}

// FilterText is the text of f; a group of and, or a not of more than one filter, has none: "".
func FilterText(f *queryv1.Filter) string {
	switch w := f.GetWhere().(type) {
	case *queryv1.Filter_Null:
		return w.Null.GetRef() + "=" + null
	case *queryv1.Filter_Bool:
		return w.Bool.GetRef() + "=" + strconv.FormatBool(w.Bool.GetValue())
	case *queryv1.Filter_String_:
		op := map[queryv1.StringOp]string{queryv1.StringOp_STRING_OP_CONTAINS: "*=",
			queryv1.StringOp_STRING_OP_BEGINS_WITH: "^="}[w.String_.GetOp()]
		if op == "" {
			op = "="
		}
		return w.String_.GetRef() + op + w.String_.GetValue()
	case *queryv1.Filter_StringIn:
		values := w.StringIn.GetValues()
		if w.StringIn.GetIncludeNull() {
			values = append(values[:len(values):len(values)], null)
		}
		return w.StringIn.GetRef() + "=" + strings.Join(values, "|")
	case *queryv1.Filter_Number:
		n := w.Number
		return n.GetRef() + opTexts[n.GetOp()] + strconv.FormatFloat(n.GetValue(), 'g', -1, 64)
	case *queryv1.Filter_Time:
		t := w.Time
		return t.GetRef() + opTexts[t.GetOp()] + t.GetValue().AsTime().Format(time.RFC3339Nano)
	case *queryv1.Filter_Duration:
		d := w.Duration
		return d.GetRef() + opTexts[d.GetOp()] + d.GetValue().AsDuration().String()
	case *queryv1.Filter_Group:
		return groupText(w.Group)
	}
	return ""
}

// groupText writes a not of one filter (!=), or an or of equalities on one ref (a|b): what Filter reads.
func groupText(g *queryv1.FilterGroup) string {
	switch g.GetOp() {
	case queryv1.FilterGroupOp_FILTER_GROUP_OP_NOT:
		if len(g.GetFilters()) != 1 {
			return ""
		}
		inner := FilterText(g.GetFilters()[0])
		ref, rest := splitRef(inner)
		if !strings.HasPrefix(rest, "=") {
			return ""
		}
		return ref + "!" + rest
	case queryv1.FilterGroupOp_FILTER_GROUP_OP_OR:
		ref, values := "", []string{}
		for _, f := range g.GetFilters() {
			r, rest := splitRef(FilterText(f))
			if !strings.HasPrefix(rest, "=") || (ref != "" && r != ref) {
				return ""
			}
			ref, values = r, append(values, rest[1:])
		}
		return ref + "=" + strings.Join(values, "|")
	}
	return ""
}

// OrderText is the text of o.
func OrderText(o *queryv1.Order) string {
	if o.GetDesc() {
		return "-" + o.GetRef()
	}
	return o.GetRef()
}

// GroupText is the text of g.
func GroupText(g *queryv1.GroupBy) string {
	switch b := g.GetBucket().(type) {
	case *queryv1.GroupBy_Period:
		return g.GetRef() + "::" + strings.ToLower(strings.TrimPrefix(b.Period.String(), "PERIOD_"))
	case *queryv1.GroupBy_NumberBins:
		bounds := make([]string, len(b.NumberBins.GetBounds()))
		for i, n := range b.NumberBins.GetBounds() {
			bounds[i] = strconv.FormatFloat(n, 'g', -1, 64)
		}
		return g.GetRef() + "::bins(" + strings.Join(bounds, ",") + ")"
	case *queryv1.GroupBy_DurationBins:
		bounds := make([]string, len(b.DurationBins.GetBounds()))
		for i, d := range b.DurationBins.GetBounds() {
			bounds[i] = d.AsDuration().String()
		}
		return g.GetRef() + "::bins(" + strings.Join(bounds, ",") + ")"
	}
	return g.GetRef()
}

// MeasureText is the text of m.
func MeasureText(m *queryv1.Measure) string {
	fn := m.GetFn()
	if fn == queryv1.MeasureFn_MEASURE_FN_UNSPECIFIED {
		fn = queryv1.MeasureFn_MEASURE_FN_COUNT
	}
	body := strings.ToLower(strings.TrimPrefix(fn.String(), "MEASURE_FN_"))
	if m.GetRef() != "" {
		body = m.GetRef() + "::" + body
	}
	if m.GetName() != "" {
		return m.GetName() + "=" + body
	}
	return body
}
