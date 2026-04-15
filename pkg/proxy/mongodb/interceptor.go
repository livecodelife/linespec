package mongodb

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/livecodelife/linespec/pkg/dsl"
	"github.com/livecodelife/linespec/pkg/logger"
	"github.com/livecodelife/linespec/pkg/registry"
	"github.com/livecodelife/linespec/pkg/types"
)

const (
	opQuery = int32(2004) // legacy — hello / isMaster; forwarded transparently
	opMsg   = int32(2013) // modern wire protocol used for all commands
)

// Interceptor is a MongoDB wire-protocol proxy. It sits between the service
// under test and a real MongoDB container. For each OP_MSG command it checks
// the mock registry; if a mock is found it synthesises a BSON response,
// otherwise the bytes are forwarded to the real upstream.
type Interceptor struct {
	addr         string
	upstreamAddr string
	registry     *registry.MockRegistry
	loader       *dsl.PayloadLoader
}

// NewInterceptor creates a new MongoDB interceptor.
func NewInterceptor(addr, upstreamAddr string, reg *registry.MockRegistry) *Interceptor {
	return &Interceptor{
		addr:         addr,
		upstreamAddr: upstreamAddr,
		registry:     reg,
		loader:       dsl.NewPayloadLoader(""),
	}
}

// Start begins accepting connections. It blocks until ctx is cancelled.
func (p *Interceptor) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", p.addr)
	if err != nil {
		return fmt.Errorf("mongodb proxy: listen %s: %w", p.addr, err)
	}
	defer ln.Close()

	logger.Info("MongoDB proxy listening on %s -> %s", p.addr, p.upstreamAddr)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return fmt.Errorf("mongodb proxy: accept: %w", err)
			}
		}
		go p.handleConn(ctx, conn)
	}
}

// handleConn manages a single client connection.
func (p *Interceptor) handleConn(ctx context.Context, clientConn net.Conn) {
	defer clientConn.Close()

	upstream, err := net.DialTimeout("tcp", p.upstreamAddr, 5*time.Second)
	if err != nil {
		logger.Error("MongoDB proxy: connect upstream %s: %v", p.upstreamAddr, err)
		return
	}
	defer upstream.Close()

	// Goroutine: copy all upstream -> client bytes (handles auth handshake transparently).
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		io.Copy(clientConn, upstream) //nolint:errcheck
	}()

	// Main loop: read client messages, intercept or forward.
	for {
		hdr, body, err := readMessage(clientConn)
		if err != nil {
			break
		}

		if hdr.opCode != opMsg {
			// Forward OP_QUERY and anything else verbatim (hello, isMaster).
			if err := writeRaw(upstream, hdr, body); err != nil {
				break
			}
			continue
		}

		mock, cmdUpper, collection, database := p.matchMock(body)
		if mock != nil {
			p.registry.CheckNegativeMocks(collection, cmdUpper)
			resp, err := p.buildResponse(mock, hdr.requestID, database, collection)
			if err != nil {
				logger.Error("MongoDB proxy: build response for [%s.%s]: %v", collection, cmdUpper, err)
				if writeErr := writeRaw(upstream, hdr, body); writeErr != nil {
					break
				}
				continue
			}
			if _, err := clientConn.Write(resp); err != nil {
				break
			}
			logger.Debug("MongoDB proxy: served mock for [%s] on collection [%s]", cmdUpper, collection)
		} else {
			// No mock — record passthrough and forward to upstream.
			p.registry.RecordPassthrough(fmt.Sprintf("%s.%s", collection, cmdUpper))
			p.registry.CheckNegativeMocks(collection, cmdUpper)
			if err := writeRaw(upstream, hdr, body); err != nil {
				break
			}
		}
	}

	upstream.Close()
	wg.Wait()
}

// msgHeader holds the 16-byte MongoDB message header fields.
type msgHeader struct {
	messageLength int32
	requestID     int32
	responseTo    int32
	opCode        int32
}

// readMessage reads a complete MongoDB message from r.
func readMessage(r io.Reader) (msgHeader, []byte, error) {
	var hdrBytes [16]byte
	if _, err := io.ReadFull(r, hdrBytes[:]); err != nil {
		return msgHeader{}, nil, err
	}
	hdr := msgHeader{
		messageLength: int32(binary.LittleEndian.Uint32(hdrBytes[0:4])),
		requestID:     int32(binary.LittleEndian.Uint32(hdrBytes[4:8])),
		responseTo:    int32(binary.LittleEndian.Uint32(hdrBytes[8:12])),
		opCode:        int32(binary.LittleEndian.Uint32(hdrBytes[12:16])),
	}
	bodyLen := int(hdr.messageLength) - 16
	if bodyLen < 0 {
		return msgHeader{}, nil, fmt.Errorf("invalid message length %d", hdr.messageLength)
	}
	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(r, body); err != nil {
		return msgHeader{}, nil, err
	}
	return hdr, body, nil
}

// writeRaw writes a complete MongoDB message (header + body) to w.
func writeRaw(w io.Writer, hdr msgHeader, body []byte) error {
	out := make([]byte, 16+len(body))
	binary.LittleEndian.PutUint32(out[0:4], uint32(hdr.messageLength))
	binary.LittleEndian.PutUint32(out[4:8], uint32(hdr.requestID))
	binary.LittleEndian.PutUint32(out[8:12], uint32(hdr.responseTo))
	binary.LittleEndian.PutUint32(out[12:16], uint32(hdr.opCode))
	copy(out[16:], body)
	_, err := w.Write(out)
	return err
}

// matchMock parses an OP_MSG body, extracts the command, collection, and database,
// then looks up the mock registry. Returns (mock, commandUpper, collection, database).
func (p *Interceptor) matchMock(body []byte) (*types.ExpectStatement, string, string, string) {
	if len(body) < 5 {
		return nil, "", "", ""
	}

	// OP_MSG body: 4-byte flagBits, then one or more sections.
	// We only handle section kind=0 (single BSON document body).
	pos := 4 // skip flagBits
	if pos >= len(body) || body[pos] != 0 {
		return nil, "", "", ""
	}
	pos++ // consume section kind byte

	_, command, collection, database := parseMsgBody(body[pos:])
	if command == "" || collection == "" {
		return nil, "", "", ""
	}

	cmdUpper := strings.ToUpper(command)
	mock, found := p.registry.FindMock(collection, cmdUpper)
	if found {
		return mock, cmdUpper, collection, database
	}
	return nil, cmdUpper, collection, database
}

// parseMsgBody decodes the BSON document from an OP_MSG section kind=0 payload.
func parseMsgBody(data []byte) (bson.D, string, string, string) {
	if len(data) < 4 {
		return nil, "", "", ""
	}
	docLen := int(binary.LittleEndian.Uint32(data[0:4]))
	if docLen > len(data) || docLen < 5 {
		return nil, "", "", ""
	}

	var doc bson.D
	if err := bson.Unmarshal(data[:docLen], &doc); err != nil {
		logger.Debug("MongoDB proxy: bson unmarshal error: %v", err)
		return nil, "", "", ""
	}

	var command, collection, database string
	if len(doc) > 0 {
		command = doc[0].Key
		if v, ok := doc[0].Value.(string); ok {
			collection = v
		}
	}
	for _, elem := range doc {
		if elem.Key == "$db" {
			if v, ok := elem.Value.(string); ok {
				database = v
			}
		}
	}

	return doc, command, collection, database
}

// buildResponse synthesises a MongoDB OP_MSG response for a matched mock.
func (p *Interceptor) buildResponse(mock *types.ExpectStatement, clientRequestID int32, database, collection string) ([]byte, error) {
	var responseDoc bson.D

	isWrite := mock.Channel == types.WriteMongoDB

	if isWrite {
		// Write commands always return a success acknowledgement.
		responseDoc = bson.D{
			{Key: "n", Value: int32(1)},
			{Key: "ok", Value: float64(1)},
		}
	} else if mock.ReturnsEmpty {
		responseDoc = bson.D{
			{Key: "cursor", Value: bson.D{
				{Key: "firstBatch", Value: bson.A{}},
				{Key: "id", Value: int64(0)},
				{Key: "ns", Value: database + "." + collection},
			}},
			{Key: "ok", Value: float64(1)},
		}
	} else if mock.ReturnsFile != "" {
		p.loader.BaseDir = mock.BaseDir
		payload, err := p.loader.Load(mock.ReturnsFile)
		if err != nil {
			return nil, fmt.Errorf("load payload %s: %w", mock.ReturnsFile, err)
		}
		docs, err := payloadToBSONDocs(payload)
		if err != nil {
			return nil, fmt.Errorf("convert payload to BSON: %w", err)
		}
		batch := make(bson.A, len(docs))
		for i, d := range docs {
			batch[i] = d
		}
		responseDoc = bson.D{
			{Key: "cursor", Value: bson.D{
				{Key: "firstBatch", Value: batch},
				{Key: "id", Value: int64(0)},
				{Key: "ns", Value: database + "." + collection},
			}},
			{Key: "ok", Value: float64(1)},
		}
	} else {
		// No payload for a read — return empty cursor.
		responseDoc = bson.D{
			{Key: "cursor", Value: bson.D{
				{Key: "firstBatch", Value: bson.A{}},
				{Key: "id", Value: int64(0)},
				{Key: "ns", Value: database + "." + collection},
			}},
			{Key: "ok", Value: float64(1)},
		}
	}

	return encodeOpMsg(clientRequestID, responseDoc)
}

// payloadToBSONDocs converts a loaded payload (map or slice) into BSON documents.
func payloadToBSONDocs(payload interface{}) ([]bson.D, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	// Try {rows: [...]} format first.
	var wrapper struct {
		Rows []json.RawMessage `json:"rows"`
	}
	if err := json.Unmarshal(raw, &wrapper); err == nil && len(wrapper.Rows) > 0 {
		docs := make([]bson.D, 0, len(wrapper.Rows))
		for _, r := range wrapper.Rows {
			d, err := jsonToBSONDoc(r)
			if err != nil {
				return nil, err
			}
			docs = append(docs, d)
		}
		return docs, nil
	}

	// Single document.
	doc, err := jsonToBSONDoc(raw)
	if err != nil {
		return nil, err
	}
	return []bson.D{doc}, nil
}

// jsonToBSONDoc converts a JSON object (as raw bytes) into a bson.D document.
func jsonToBSONDoc(raw []byte) (bson.D, error) {
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return mapToBSONDoc(m), nil
}

// mapToBSONDoc converts a map[string]interface{} to bson.D.
// It maps JSON "id" fields that look like 24-char hex ObjectIDs to BSON "_id: ObjectID",
// so payloads written in JSON API format work correctly with the MongoDB wire protocol.
func mapToBSONDoc(m map[string]interface{}) bson.D {
	doc := make(bson.D, 0, len(m))
	for k, v := range m {
		key := k
		val := normaliseValue(v)
		if k == "id" {
			if s, ok := v.(string); ok && len(s) == 24 {
				if oid, err := bson.ObjectIDFromHex(s); err == nil {
					key = "_id"
					val = oid
				}
			}
		}
		doc = append(doc, bson.E{Key: key, Value: val})
	}
	return doc
}

// normaliseValue recursively converts interface{} values to BSON-compatible types.
func normaliseValue(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		return mapToBSONDoc(val)
	case []interface{}:
		a := make(bson.A, len(val))
		for i, item := range val {
			a[i] = normaliseValue(item)
		}
		return a
	case float64:
		if val == float64(int32(val)) && val >= -2147483648 && val <= 2147483647 {
			return int32(val)
		}
		return val
	default:
		return v
	}
}

// encodeOpMsg wraps a BSON document in an OP_MSG response frame.
func encodeOpMsg(responseTo int32, doc bson.D) ([]byte, error) {
	docBytes, err := bson.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("bson marshal: %w", err)
	}

	// OP_MSG: [header 16] + [flagBits 4] + [sectionKind 1] + [bsonDoc N]
	total := 16 + 4 + 1 + len(docBytes)
	out := make([]byte, total)
	binary.LittleEndian.PutUint32(out[0:4], uint32(total))
	binary.LittleEndian.PutUint32(out[4:8], 0)                   // requestID (server-generated)
	binary.LittleEndian.PutUint32(out[8:12], uint32(responseTo)) // responseTo
	binary.LittleEndian.PutUint32(out[12:16], uint32(opMsg))
	binary.LittleEndian.PutUint32(out[16:20], 0) // flagBits = 0
	out[20] = 0                                   // section kind = 0 (body)
	copy(out[21:], docBytes)
	return out, nil
}
