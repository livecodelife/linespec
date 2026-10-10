package postgresql

// Tests for prov-2026-becc5d88: answer Parse locally when a value-level VERIFY
// mock (VERIFY_WHERE <col>: <val>, VERIFY_WRITTEN_VALUES) could match the query
// SHAPE, so a pipelined P/B/D/E/S (Npgsql) sees ParseComplete before
// BindComplete. Values are checked at Bind.
//
// Interface these tests define (does not exist yet):
//
//	type parseAction int
//	const (
//	    parseActionLocal   parseAction = iota // answer ParseComplete locally, mark mock-only
//	    parseActionForward                    // forward Parse upstream
//	)
//
//	func parseDisposition(shapeMatchable, canResolveOIDs bool) parseAction
//	    pure. parseActionLocal iff shapeMatchable && canResolveOIDs. The
//	    tokio-postgres exception (canResolveOIDs == false -> forward) is kept.
//
//	func (p *Proxy) peekMockShape(query string) (*types.ExpectStatement, bool)
//	    shape-only variant of peekMock(query, nil): honours table set, operation
//	    (VERIFY_OPERATION and READ/WRITE direction) and VERIFY_WHERE_COLUMNS, but
//	    IGNORES value-level predicates (VERIFY_WHERE values, VERIFY_WRITTEN_VALUES).
//	    Does not increment hit counts.
//
// The proxy loop (handleClientMessagesWithInterception) is also driven end to
// end with a recorded Npgsql-style byte transcript, through real connections,
// asserting the frames the client sees and the frames upstream sees.
// Candidate for the per-driver conformance corpus (prov-2026-167380f0).

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/livecodelife/linespec/v3/pkg/registry"
	"github.com/livecodelife/linespec/v3/pkg/types"
)

const (
	pgoInsertSQL = "INSERT INTO sponsors (sponsorid) VALUES ($1)"
	pgoSelectSQL = "SELECT id, sponsorid FROM sponsors WHERE sponsorid = $1"
	pgoOIDInt8   = uint32(20)
)

type pgoFrame struct {
	typ     byte
	payload []byte
}

func pgoMsg(typ byte, payload []byte) []byte {
	out := []byte{typ}
	out = binary.BigEndian.AppendUint32(out, uint32(len(payload)+4))
	return append(out, payload...)
}

// pgoParse builds a Parse frame for the unnamed statement with explicit OIDs.
func pgoParse(query string, oids ...uint32) []byte {
	p := []byte{0}
	p = append(p, query...)
	p = append(p, 0)
	p = binary.BigEndian.AppendUint16(p, uint16(len(oids)))
	for _, o := range oids {
		p = binary.BigEndian.AppendUint32(p, o)
	}
	return pgoMsg('P', p)
}

// pgoBindInt8 builds a Bind frame: unnamed portal+statement, one binary int8 param.
func pgoBindInt8(v uint64) []byte {
	return pgoMsg('B', bpBindFrame([]int16{1}, [][]byte{bpBE64(v)}))
}

func pgoDescribePortal() []byte { return pgoMsg('D', []byte{'P', 0}) }
func pgoExecute() []byte        { return pgoMsg('E', []byte{0, 0, 0, 0, 0}) }
func pgoSync() []byte           { return pgoMsg('S', nil) }

func pgoCat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func pgoSplit(t *testing.T, raw []byte) []pgoFrame {
	t.Helper()
	var out []pgoFrame
	for len(raw) > 0 {
		if len(raw) < 5 {
			t.Fatalf("truncated frame header in %q", raw)
		}
		n := int(binary.BigEndian.Uint32(raw[1:5]))
		if n < 4 || len(raw) < 1+n {
			t.Fatalf("bad frame length %d in %q", n, raw)
		}
		out = append(out, pgoFrame{raw[0], raw[5 : 1+n]})
		raw = raw[1+n:]
	}
	return out
}

func pgoTypes(frames []pgoFrame) string {
	var b strings.Builder
	for _, f := range frames {
		b.WriteByte(f.typ)
	}
	return b.String()
}

func pgoDrain(t *testing.T, c net.Conn) []byte {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	b, err := io.ReadAll(c)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	return b
}

func pgoProxy(t *testing.T, expects ...types.ExpectStatement) *Proxy {
	t.Helper()
	reg := registry.NewMockRegistry()
	reg.Register(&types.TestSpec{Name: "parse-ordering", Expects: expects})
	return NewProxy("localhost:5432", "localhost:5433", reg)
}

// pgoRun replays transcript through the proxy loop and returns the frames the
// client saw and the frames that reached upstream.
func pgoRun(t *testing.T, p *Proxy, transcript []byte) (client, upstream []pgoFrame) {
	t.Helper()
	clientFar, clientNear := dialPipe(t)
	upFar, upNear := dialPipe(t)
	p.handleClientMessagesWithInterception(bytes.NewReader(transcript), upNear, clientNear)
	clientNear.Close()
	upNear.Close()
	client = pgoSplit(t, pgoDrain(t, clientFar))
	upstream = pgoSplit(t, pgoDrain(t, upFar))
	clientFar.Close()
	upFar.Close()
	return client, upstream
}

func pgoWriteMock(written map[string]string) types.ExpectStatement {
	return types.ExpectStatement{
		Channel:             types.WritePostgreSQL,
		AccessingTables:     []string{"sponsors"},
		VerifyOperation:     "INSERT",
		VerifyWrittenValues: written,
	}
}

func pgoReadMock(where map[string]string, whereCols []string) types.ExpectStatement {
	return types.ExpectStatement{
		Channel:            types.ReadPostgreSQL,
		AccessingTables:    []string{"sponsors"},
		VerifyOperation:    "SELECT",
		VerifyWhere:        where,
		VerifyWhereColumns: whereCols,
		ReturnsEmpty:       true,
	}
}

func pgoIndex(s string, b byte) int { return strings.IndexByte(s, b) }

// (1) Parse disposition is a pure function of (shape-matchable, canResolveOIDs).
func TestParseOrdering_DispositionTable(t *testing.T) {
	cases := []struct {
		shape, resolve bool
		want           parseAction
	}{
		{true, true, parseActionLocal},
		{true, false, parseActionForward}, // tokio-postgres exception
		{false, true, parseActionForward},
		{false, false, parseActionForward},
	}
	for _, c := range cases {
		if got := parseDisposition(c.shape, c.resolve); got != c.want {
			t.Errorf("parseDisposition(shape=%v, canResolveOIDs=%v) = %v, want %v", c.shape, c.resolve, got, c.want)
		}
	}
}

// (2) Shape-only check ignores value-level predicates.
func TestParseOrdering_ShapeIgnoresWrittenValues(t *testing.T) {
	p := pgoProxy(t, pgoWriteMock(map[string]string{"sponsorid": "7"}))
	// Precondition that motivates the record: the value-level peek misses with no binds.
	if _, ok := p.peekMock(pgoInsertSQL, nil); ok {
		t.Fatal("precondition: peekMock(query, nil) should miss a VERIFY_WRITTEN_VALUES mock")
	}
	if _, ok := p.peekMockShape(pgoInsertSQL); !ok {
		t.Error("peekMockShape should match: table and operation fit, only written values are unchecked")
	}
}

func TestParseOrdering_ShapeIgnoresWhereValues(t *testing.T) {
	p := pgoProxy(t, pgoReadMock(map[string]string{"sponsorid": "7"}, nil))
	if _, ok := p.peekMockShape(pgoSelectSQL); !ok {
		t.Error("peekMockShape should match: only the VERIFY_WHERE value is unchecked")
	}
}

func TestParseOrdering_ShapeDoesNotConsumeHit(t *testing.T) {
	p := pgoProxy(t, pgoWriteMock(map[string]string{"sponsorid": "7"}))
	for i := 0; i < 3; i++ {
		if _, ok := p.peekMockShape(pgoInsertSQL); !ok {
			t.Fatalf("peek %d: shape should keep matching (no hit consumed)", i)
		}
	}
}

// Shape still honours table, operation, and where columns.
func TestParseOrdering_ShapeStillHonoursTableOperationColumns(t *testing.T) {
	t.Run("operation mismatch", func(t *testing.T) {
		p := pgoProxy(t, pgoWriteMock(map[string]string{"sponsorid": "7"}))
		if _, ok := p.peekMockShape("UPDATE sponsors SET sponsorid = $1 WHERE id = $2"); ok {
			t.Error("INSERT mock must not shape-match an UPDATE")
		}
	})
	t.Run("direction mismatch", func(t *testing.T) {
		p := pgoProxy(t, pgoReadMock(map[string]string{"sponsorid": "7"}, nil))
		if _, ok := p.peekMockShape(pgoInsertSQL); ok {
			t.Error("READ SELECT mock must not shape-match an INSERT")
		}
	})
	t.Run("table mismatch", func(t *testing.T) {
		p := pgoProxy(t, pgoWriteMock(map[string]string{"sponsorid": "7"}))
		if _, ok := p.peekMockShape("INSERT INTO donors (sponsorid) VALUES ($1)"); ok {
			t.Error("mock for sponsors must not shape-match a query on another table")
		}
	})
	t.Run("where columns mismatch", func(t *testing.T) {
		p := pgoProxy(t, pgoReadMock(map[string]string{"sponsorid": "7"}, []string{"sponsorid"}))
		if _, ok := p.peekMockShape("SELECT id FROM sponsors WHERE id = $1"); ok {
			t.Error("VERIFY_WHERE_COLUMNS sponsorid is column shape and must still be enforced")
		}
	})
	t.Run("where columns present", func(t *testing.T) {
		p := pgoProxy(t, pgoReadMock(map[string]string{"sponsorid": "7"}, []string{"sponsorid"}))
		if _, ok := p.peekMockShape(pgoSelectSQL); !ok {
			t.Error("VERIFY_WHERE_COLUMNS satisfied: shape should match")
		}
	})
	t.Run("no mocks", func(t *testing.T) {
		p := pgoProxy(t)
		if _, ok := p.peekMockShape(pgoInsertSQL); ok {
			t.Error("no mocks registered: no shape match")
		}
	})
}

// (3) Recorded Npgsql pipelined P/B/D/E/S, int8 sponsorid=7 INSERT with a
// VERIFY_WRITTEN_VALUES mock. Client must see ParseComplete before BindComplete.
func TestParseOrdering_NpgsqlInsertPipelinedParseCompleteBeforeBindComplete(t *testing.T) {
	p := pgoProxy(t, pgoWriteMock(map[string]string{"sponsorid": "7"}))
	transcript := pgoCat(
		pgoParse(pgoInsertSQL, pgoOIDInt8),
		pgoBindInt8(7),
		pgoDescribePortal(),
		pgoExecute(),
		pgoSync(),
	)
	client, upstream := pgoRun(t, p, transcript)
	got := pgoTypes(client)
	if !strings.HasPrefix(got, "12") {
		t.Fatalf("client frames = %q, want ParseComplete('1') then BindComplete('2') first", got)
	}
	if strings.Contains(got, "E") {
		t.Errorf("client saw an ErrorResponse: %q", got)
	}
	if !strings.HasSuffix(got, "Z") {
		t.Errorf("client frames = %q, want to end with ReadyForQuery", got)
	}
	if strings.Count(got, "1") != 1 {
		t.Errorf("client frames = %q, want exactly one ParseComplete (no duplicate)", got)
	}
	for _, f := range upstream {
		if f.typ == 'P' || f.typ == 'B' {
			t.Errorf("upstream received %q; statement is mock-only and must not reach upstream", string(f.typ))
		}
	}
}

// Same, for a SELECT with VERIFY_WHERE sponsorid: 7.
func TestParseOrdering_NpgsqlSelectPipelinedParseCompleteBeforeBindComplete(t *testing.T) {
	p := pgoProxy(t, pgoReadMock(map[string]string{"sponsorid": "7"}, nil))
	transcript := pgoCat(
		pgoParse(pgoSelectSQL, pgoOIDInt8),
		pgoBindInt8(7),
		pgoDescribePortal(),
		pgoExecute(),
		pgoSync(),
	)
	client, upstream := pgoRun(t, p, transcript)
	got := pgoTypes(client)
	if !strings.HasPrefix(got, "12") {
		t.Fatalf("client frames = %q, want ParseComplete('1') then BindComplete('2') first", got)
	}
	if i, j := pgoIndex(got, '1'), pgoIndex(got, '2'); i < 0 || j < 0 || i > j {
		t.Errorf("ParseComplete must precede BindComplete: %q", got)
	}
	if strings.Contains(got, "E") {
		t.Errorf("client saw an ErrorResponse: %q", got)
	}
	for _, f := range upstream {
		if f.typ == 'P' || f.typ == 'B' {
			t.Errorf("upstream received %q; statement is mock-only", string(f.typ))
		}
	}
}

// (4) Mocks without value-level VERIFY behave exactly as today.
func TestParseOrdering_PlainMockUnchanged(t *testing.T) {
	p := pgoProxy(t, types.ExpectStatement{
		Channel:         types.WritePostgreSQL,
		AccessingTables: []string{"sponsors"},
		VerifyOperation: "INSERT",
	})
	if _, ok := p.peekMock(pgoInsertSQL, nil); !ok {
		t.Fatal("precondition: plain mock matches at Parse today")
	}
	client, upstream := pgoRun(t, p, pgoCat(
		pgoParse(pgoInsertSQL, pgoOIDInt8),
		pgoBindInt8(7),
		pgoDescribePortal(),
		pgoExecute(),
		pgoSync(),
	))
	got := pgoTypes(client)
	if !strings.HasPrefix(got, "12") || strings.Contains(got, "E") || !strings.HasSuffix(got, "Z") {
		t.Errorf("client frames = %q, want 1,2,... Z with no error", got)
	}
	for _, f := range upstream {
		if f.typ == 'P' || f.typ == 'B' {
			t.Errorf("upstream received %q", string(f.typ))
		}
	}
}

// With no mock for the query at all, Parse and Bind are forwarded, nothing is local.
func TestParseOrdering_NoMockForwardsParseAndBind(t *testing.T) {
	p := pgoProxy(t, pgoWriteMock(map[string]string{"sponsorid": "7"}))
	const other = "INSERT INTO donors (donorid) VALUES ($1)"
	client, upstream := pgoRun(t, p, pgoCat(pgoParse(other, pgoOIDInt8), pgoBindInt8(7)))
	if len(client) != 0 {
		t.Errorf("client frames = %q, want none (unmocked statement is relayed)", pgoTypes(client))
	}
	if got := pgoTypes(upstream); got != "PB" {
		t.Errorf("upstream frames = %q, want \"PB\"", got)
	}
}

// tokio-postgres exception stays: parameters whose OIDs cannot be resolved
// (no explicit OIDs, no $N::TYPE cast) forward Parse even when the shape matches.
func TestParseOrdering_UnresolvableOIDsForwardParse(t *testing.T) {
	p := pgoProxy(t, pgoWriteMock(map[string]string{"sponsorid": "7"}))
	client, upstream := pgoRun(t, p, pgoParse(pgoInsertSQL /* no OIDs */))
	if len(client) != 0 {
		t.Errorf("client frames = %q, want no local ParseComplete", pgoTypes(client))
	}
	if got := pgoTypes(upstream); got != "P" {
		t.Errorf("upstream frames = %q, want Parse forwarded", got)
	}
}

// (5) DECIDED: Bind values match no mock for a statement already answered
// locally -> ErrorResponse "no mock matched bind values", Bind NOT forwarded.
func TestParseOrdering_UnmatchedBindReturnsErrorAndIsNotForwarded(t *testing.T) {
	p := pgoProxy(t, pgoWriteMock(map[string]string{"sponsorid": "7"}))
	client, upstream := pgoRun(t, p, pgoCat(
		pgoParse(pgoInsertSQL, pgoOIDInt8),
		pgoBindInt8(99), // wrong value: no mock matches
		pgoSync(),
	))
	if len(client) < 2 || client[0].typ != '1' || client[1].typ != 'E' {
		t.Fatalf("client frames = %q, want ParseComplete then ErrorResponse", pgoTypes(client))
	}
	if !bytes.Contains(client[1].payload, []byte("no mock matched bind values")) {
		t.Errorf("ErrorResponse payload %q does not contain %q", client[1].payload, "no mock matched bind values")
	}
	if strings.Contains(pgoTypes(client), "2") {
		t.Errorf("client saw BindComplete for an unmatched Bind: %q", pgoTypes(client))
	}
	for _, f := range upstream {
		if f.typ == 'B' || f.typ == 'P' {
			t.Errorf("upstream received %q; upstream never saw Parse so Bind must not be forwarded", string(f.typ))
		}
	}
}

// Same decision for the VERIFY_WHERE SELECT shape.
func TestParseOrdering_UnmatchedBindWhereReturnsError(t *testing.T) {
	p := pgoProxy(t, pgoReadMock(map[string]string{"sponsorid": "7"}, nil))
	client, upstream := pgoRun(t, p, pgoCat(
		pgoParse(pgoSelectSQL, pgoOIDInt8),
		pgoBindInt8(99),
		pgoSync(),
	))
	if len(client) < 2 || client[0].typ != '1' || client[1].typ != 'E' {
		t.Fatalf("client frames = %q, want ParseComplete then ErrorResponse", pgoTypes(client))
	}
	if !bytes.Contains(client[1].payload, []byte("no mock matched bind values")) {
		t.Errorf("ErrorResponse payload %q lacks the clear message", client[1].payload)
	}
	for _, f := range upstream {
		if f.typ == 'B' || f.typ == 'P' {
			t.Errorf("upstream received %q", string(f.typ))
		}
	}
}

// (6) After the unmatched-Bind ErrorResponse the connection stays consistent:
// Describe and Execute are swallowed (upstream never saw Parse/Bind) and the
// Sync is answered locally with ReadyForQuery. Client sees exactly 1,E,Z.
func TestParseOrdering_UnmatchedBindPipelineStaysConsistentFullPipeline(t *testing.T) {
	p := pgoProxy(t, pgoWriteMock(map[string]string{"sponsorid": "7"}))
	client, upstream := pgoRun(t, p, pgoCat(
		pgoParse(pgoInsertSQL, pgoOIDInt8),
		pgoBindInt8(99), // no mock matches
		pgoDescribePortal(),
		pgoExecute(),
		pgoSync(),
	))
	if got := pgoTypes(client); got != "1EZ" {
		t.Fatalf("client frames = %q, want exactly \"1EZ\" (ParseComplete, ErrorResponse, ReadyForQuery)", got)
	}
	if got := string(client[2].payload); got != "I" {
		t.Errorf("ReadyForQuery tx status = %q, want \"I\" (idle)", got)
	}
	if got := pgoTypes(upstream); got != "" {
		t.Errorf("upstream frames = %q, want none (P, B, D, E and S must all stay local)", got)
	}
}

// A second, matching pipeline on the same connection works normally: the
// swallow flag is cleared by the first Sync.
func TestParseOrdering_UnmatchedBindPipelineStaysConsistentSecondPipelineMatches(t *testing.T) {
	p := pgoProxy(t, pgoWriteMock(map[string]string{"sponsorid": "7"}))
	client, upstream := pgoRun(t, p, pgoCat(
		pgoParse(pgoInsertSQL, pgoOIDInt8),
		pgoBindInt8(99),
		pgoDescribePortal(),
		pgoExecute(),
		pgoSync(),
		pgoParse(pgoInsertSQL, pgoOIDInt8),
		pgoBindInt8(7), // matches
		pgoDescribePortal(),
		pgoExecute(),
		pgoSync(),
	))
	got := pgoTypes(client)
	if !strings.HasPrefix(got, "1EZ12") {
		t.Fatalf("client frames = %q, want first pipeline \"1EZ\" then second pipeline starting \"12\"", got)
	}
	if !strings.HasSuffix(got, "Z") {
		t.Errorf("client frames = %q, want to end with ReadyForQuery", got)
	}
	if strings.Count(got, "E") != 1 {
		t.Errorf("client frames = %q, want exactly one ErrorResponse (first pipeline only)", got)
	}
	if strings.Count(got, "Z") != 2 {
		t.Errorf("client frames = %q, want one ReadyForQuery per Sync", got)
	}
	for _, f := range upstream {
		if f.typ == 'P' || f.typ == 'B' {
			t.Errorf("upstream received %q; both statements are mock-only", string(f.typ))
		}
	}
}
