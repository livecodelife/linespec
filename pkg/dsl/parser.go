package dsl

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/livecodelife/linespec/pkg/interpolate"
	"github.com/livecodelife/linespec/pkg/types"
)

type Parser struct {
	tokens   []Token
	pos      int
	Resolver *interpolate.Resolver
}

func NewParser(tokens []Token) *Parser {
	return &Parser{tokens: tokens, pos: 0}
}

// NewParserWithResolver creates a parser with a resolver for variable substitution
func NewParserWithResolver(tokens []Token, resolver *interpolate.Resolver) *Parser {
	return &Parser{tokens: tokens, pos: 0, Resolver: resolver}
}

func (p *Parser) peek() *Token {
	if p.pos >= len(p.tokens) {
		return nil
	}
	return &p.tokens[p.pos]
}

func (p *Parser) consume() *Token {
	token := p.peek()
	if token != nil {
		p.pos++
	}
	return token
}

func (p *Parser) expect(tType TokenType) (*Token, error) {
	token := p.consume()
	if token == nil || token.Type != tType {
		msg := fmt.Sprintf("Expected %s but got %v", tType, token)
		if token != nil {
			return nil, fmt.Errorf("Parser error at line %d: %s", token.Line, msg)
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return token, nil
}

func (p *Parser) Parse(filename string) (*types.TestSpec, error) {
	spec := &types.TestSpec{}
	spec.FilePath = filename
	spec.BaseDir = filepath.Dir(filename)

	if p.peek().Type == TokenTest {
		token := p.consume()
		spec.Name = token.Literal
	} else {
		spec.Name = strings.TrimSuffix(filepath.Base(filename), ".linespec")
	}

	receiveToken, err := p.expect(TokenReceive)
	if err != nil {
		return nil, err
	}

	reHttp := regexp.MustCompile(`(?i)^HTTP:(\w+)\s+(.+)$`)
	reKafka := regexp.MustCompile(`(?i)^(?:KAFKA|EVENT):(.+)$`)
	reGRPCReceive := regexp.MustCompile(`(?i)^GRPC:(.+)/(\w+)$`)

	if mHttp := reHttp.FindStringSubmatch(receiveToken.Literal); mHttp != nil {
		spec.Receive.Channel = types.HTTP
		spec.Receive.Method = strings.ToUpper(mHttp[1])
		spec.Receive.Path = p.resolve(mHttp[2])
	} else if mKafka := reKafka.FindStringSubmatch(receiveToken.Literal); mKafka != nil {
		spec.Receive.Channel = types.Event
		spec.Receive.Topic = strings.TrimSpace(mKafka[1])
	} else if mGRPC := reGRPCReceive.FindStringSubmatch(receiveToken.Literal); mGRPC != nil {
		spec.Receive.Channel = types.GRPC
		spec.Receive.Service = mGRPC[1]
		spec.Receive.RPCMethod = mGRPC[2]
	} else {
		return nil, fmt.Errorf("Invalid RECEIVE format at line %d: %s", receiveToken.Line, receiveToken.Literal)
	}

	if p.peek().Type == TokenWith {
		spec.Receive.WithFile = p.consume().Literal
	}

	if p.peek().Type == TokenHeaders {
		headersToken := p.consume()
		spec.Receive.Headers = p.resolveHeaders(parseHeaders(headersToken.Literal))
	}

	for p.peek().Type == TokenExpect {
		expect, err := p.parseExpect()
		if err != nil {
			return nil, err
		}
		spec.Expects = append(spec.Expects, *expect)
	}

	for p.peek().Type == TokenExpectNot {
		expectNot, err := p.parseExpectNot()
		if err != nil {
			return nil, err
		}
		spec.ExpectsNot = append(spec.ExpectsNot, *expectNot)
	}

	// RESPOND is required for HTTP-triggered tests, optional for Kafka consumer tests.
	if p.peek() != nil && p.peek().Type == TokenRespond {
		respondToken := p.consume()

		reStatus := regexp.MustCompile(`(?i)^HTTP:(\d+)$`)
		mStatus := reStatus.FindStringSubmatch(respondToken.Literal)
		if mStatus == nil {
			return nil, fmt.Errorf("Invalid RESPOND format at line %d: %s", respondToken.Line, respondToken.Literal)
		}

		statusCode, _ := strconv.Atoi(mStatus[1])
		spec.Respond.StatusCode = statusCode

		if p.peek().Type == TokenWith {
			spec.Respond.WithFile = p.consume().Literal
		}

		if p.peek().Type == TokenNoise {
			noiseToken := p.consume()
			spec.Respond.Noise = strings.Split(strings.TrimSpace(noiseToken.Literal), "\n")
		}

		if p.peek().Type == TokenHeaders {
			headersToken := p.consume()
			spec.Respond.Headers = p.resolveHeaders(parseHeaders(headersToken.Literal))
		}
	} else if spec.Receive.Channel == types.HTTP {
		return nil, fmt.Errorf("RESPOND block is required for HTTP-triggered tests")
	}

	return spec, nil
}

// resolve applies variable substitution if a resolver is configured
func (p *Parser) resolve(s string) string {
	if p.Resolver != nil && interpolate.HasVariables(s) {
		return p.Resolver.Resolve(s)
	}
	return s
}

// resolveHeaders applies variable substitution to all header values
func (p *Parser) resolveHeaders(headers map[string]string) map[string]string {
	if p.Resolver == nil {
		return headers
	}
	return p.Resolver.ResolveMap(headers)
}

func (p *Parser) parseExpect() (*types.ExpectStatement, error) {
	token := p.consume()
	expect, err := parseExpectChannel(token.Literal, token.Line)
	if err != nil {
		return nil, err
	}

	// Apply variable substitution to channel-specific fields
	if expect.Channel == types.HTTP && expect.URL != "" {
		expect.URL = p.resolve(expect.URL)
	}
	if expect.Channel == types.ReadRedis || expect.Channel == types.WriteRedis {
		expect.RedisKey = p.resolve(expect.RedisKey)
	}
	if expect.Channel == types.GRPC {
		expect.Service = p.resolve(expect.Service)
		expect.RPCMethod = p.resolve(expect.RPCMethod)
	}

	// Handle HEADERS for HTTP expectations
	if p.peek().Type == TokenHeaders {
		headersToken := p.consume()
		expect.Headers = p.resolveHeaders(parseHeaders(headersToken.Literal))
	}

	if p.peek().Type == TokenUsingSql {
		p.consume() // TokenUsingSql
		sqlToken, err := p.expect(TokenSqlBlock)
		if err != nil {
			return nil, err
		}
		expect.SQL = p.resolve(sqlToken.Literal)
	}

	if p.peek().Type == TokenNoTransaction {
		p.consume()
		expect.NoTransaction = true
	}

	if p.peek().Type == TokenWith {
		expect.WithFile = p.consume().Literal
	}

	if p.peek().Type == TokenReturns {
		returnsToken := p.consume()
		if strings.ToUpper(returnsToken.Literal) == "EMPTY" {
			expect.ReturnsEmpty = true
		} else if m := regexp.MustCompile(`^\{\{(.+)\}\}$`).FindStringSubmatch(returnsToken.Literal); m != nil {
			expect.ReturnsFile = m[1]
		} else {
			expect.ReturnsFile = returnsToken.Literal
		}
	}

	if p.peek().Type == TokenResponseHeaders {
		headersToken := p.consume()
		expect.ResponseHeaders = p.resolveHeaders(parseHeaders(headersToken.Literal))
	}

	for p.peek().Type == TokenVerify {
		verifyToken := p.consume()
		rule, err := parseVerifyRule(verifyToken.Literal, verifyToken.Line)
		if err != nil {
			return nil, err
		}
		expect.Verify = append(expect.Verify, *rule)
	}

	return expect, nil
}

func (p *Parser) parseExpectNot() (*types.ExpectStatement, error) {
	token := p.consume()
	expect, err := parseExpectChannel(token.Literal, token.Line) // Using same logic for channel parsing
	if err != nil {
		return nil, err
	}
	expect.Negative = true

	if expect.Channel == types.ReadRedis || expect.Channel == types.WriteRedis {
		expect.RedisKey = p.resolve(expect.RedisKey)
	}
	if expect.Channel == types.GRPC {
		expect.Service = p.resolve(expect.Service)
		expect.RPCMethod = p.resolve(expect.RPCMethod)
	}

	if p.peek().Type == TokenWith {
		expect.WithFile = p.consume().Literal
	}

	return expect, nil
}

func parseHeaders(literal string) map[string]string {
	headers := make(map[string]string)
	lines := strings.Split(literal, "\n")
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			headers[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return headers
}

func parseExpectChannel(value string, line int) (*types.ExpectStatement, error) {
	// Replicates parseExpectChannel logic from TypeScript
	reEvent := regexp.MustCompile(`(?i)^(EVENT|MESSAGE):(.+)$`)
	if m := reEvent.FindStringSubmatch(value); m != nil {
		return &types.ExpectStatement{
			Channel: types.Event,
			Topic:   m[2],
		}, nil
	}

	// gRPC: GRPC:package.Service/Method (no space, matched before SplitN)
	reGRPC := regexp.MustCompile(`(?i)^GRPC:(.+)/(\w+)$`)
	if m := reGRPC.FindStringSubmatch(value); m != nil {
		return &types.ExpectStatement{
			Channel:   types.GRPC,
			Service:   m[1],
			RPCMethod: m[2],
		}, nil
	}

	parts := strings.SplitN(value, " ", 2)
	if len(parts) < 2 {
		return nil, fmt.Errorf("Invalid EXPECT channel format at line %d: %s", line, value)
	}

	channelPart := strings.ToUpper(parts[0])
	rest := parts[1]

	reHttp := regexp.MustCompile(`^HTTP:(\w+)$`)
	if m := reHttp.FindStringSubmatch(channelPart); m != nil {
		return &types.ExpectStatement{
			Channel: types.HTTP,
			Method:  strings.ToUpper(m[1]),
			URL:     rest,
		}, nil
	}

	if channelPart == "WRITE:MYSQL" {
		reOp := regexp.MustCompile(`(?i)^(INSERT|UPDATE|DELETE)\s+(.+)$`)
		if m := reOp.FindStringSubmatch(rest); m != nil {
			return &types.ExpectStatement{
				Channel: types.WriteMySQL,
				Table:   m[2],
			}, nil
		}
		return &types.ExpectStatement{
			Channel: types.WriteMySQL,
			Table:   rest,
		}, nil
	}

	if channelPart == "READ:MYSQL" {
		return &types.ExpectStatement{
			Channel: types.ReadMySQL,
			Table:   rest,
		}, nil
	}

	if channelPart == "WRITE:POSTGRESQL" {
		return &types.ExpectStatement{
			Channel: types.WritePostgreSQL,
			Table:   rest,
		}, nil
	}

	if channelPart == "READ:POSTGRESQL" {
		return &types.ExpectStatement{
			Channel: types.ReadPostgreSQL,
			Table:   rest,
		}, nil
	}

	// Redis: READ:REDIS <command> <key> or WRITE:REDIS <command> <key>
	if channelPart == "READ:REDIS" || channelPart == "WRITE:REDIS" {
		channel := types.ReadRedis
		if channelPart == "WRITE:REDIS" {
			channel = types.WriteRedis
		}
		redisParts := strings.SplitN(rest, " ", 2)
		cmd := strings.ToUpper(redisParts[0])
		key := ""
		if len(redisParts) > 1 {
			key = redisParts[1]
		}
		return &types.ExpectStatement{
			Channel:  channel,
			Command:  cmd,
			RedisKey: key,
		}, nil
	}

	return nil, fmt.Errorf("Unrecognized EXPECT channel at line %d: %s", line, channelPart)
}

func parseVerifyRule(value string, line int) (*types.VerifyRule, error) {
	// SQL targets
	reQueryContains := regexp.MustCompile(`(?i)^query\s+CONTAINS\s+['"](.+?)['"]$`)
	if m := reQueryContains.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "CONTAINS", Target: "query", Pattern: m[1]}, nil
	}

	reQueryNotContains := regexp.MustCompile(`(?i)^query\s+NOT_CONTAINS\s+['"](.+?)['"]$`)
	if m := reQueryNotContains.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "NOT_CONTAINS", Target: "query", Pattern: m[1]}, nil
	}

	reQueryMatches := regexp.MustCompile(`(?i)^query\s+MATCHES\s/(.+?)/$`)
	if m := reQueryMatches.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "MATCHES", Target: "query", Pattern: m[1]}, nil
	}

	// HTTP targets: headers.NAME
	reHeadersContains := regexp.MustCompile(`(?i)^headers\.([\w-]+)\s+CONTAINS\s+['"](.+?)['"]$`)
	if m := reHeadersContains.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "CONTAINS", Target: "headers." + m[1], Pattern: m[2]}, nil
	}

	reHeadersNotContains := regexp.MustCompile(`(?i)^headers\.([\w-]+)\s+NOT_CONTAINS\s+['"](.+?)['"]$`)
	if m := reHeadersNotContains.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "NOT_CONTAINS", Target: "headers." + m[1], Pattern: m[2]}, nil
	}

	reHeadersMatches := regexp.MustCompile(`(?i)^headers\.([\w-]+)\s+MATCHES\s/(.+?)/$`)
	if m := reHeadersMatches.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "MATCHES", Target: "headers." + m[1], Pattern: m[2]}, nil
	}

	// HTTP targets: body
	reBodyContains := regexp.MustCompile(`(?i)^body\s+CONTAINS\s+['"](.+?)['"]$`)
	if m := reBodyContains.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "CONTAINS", Target: "body", Pattern: m[1]}, nil
	}

	reBodyNotContains := regexp.MustCompile(`(?i)^body\s+NOT_CONTAINS\s+['"](.+?)['"]$`)
	if m := reBodyNotContains.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "NOT_CONTAINS", Target: "body", Pattern: m[1]}, nil
	}

	reBodyMatches := regexp.MustCompile(`(?i)^body\s+MATCHES\s/(.+?)/$`)
	if m := reBodyMatches.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "MATCHES", Target: "body", Pattern: m[1]}, nil
	}

	// HTTP targets: url
	reURLContains := regexp.MustCompile(`(?i)^url\s+CONTAINS\s+['"](.+?)['"]$`)
	if m := reURLContains.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "CONTAINS", Target: "url", Pattern: m[1]}, nil
	}

	reURLNotContains := regexp.MustCompile(`(?i)^url\s+NOT_CONTAINS\s+['"](.+?)['"]$`)
	if m := reURLNotContains.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "NOT_CONTAINS", Target: "url", Pattern: m[1]}, nil
	}

	reURLMatches := regexp.MustCompile(`(?i)^url\s+MATCHES\s/(.+?)/$`)
	if m := reURLMatches.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "MATCHES", Target: "url", Pattern: m[1]}, nil
	}

	// HTTP targets: path
	rePathContains := regexp.MustCompile(`(?i)^path\s+CONTAINS\s+['"](.+?)['"]$`)
	if m := rePathContains.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "CONTAINS", Target: "path", Pattern: m[1]}, nil
	}

	rePathNotContains := regexp.MustCompile(`(?i)^path\s+NOT_CONTAINS\s+['"](.+?)['"]$`)
	if m := rePathNotContains.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "NOT_CONTAINS", Target: "path", Pattern: m[1]}, nil
	}

	rePathMatches := regexp.MustCompile(`(?i)^path\s+MATCHES\s/(.+?)/$`)
	if m := rePathMatches.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "MATCHES", Target: "path", Pattern: m[1]}, nil
	}

	// Kafka targets: key
	reKeyContains := regexp.MustCompile(`(?i)^key\s+CONTAINS\s+['"](.+?)['"]$`)
	if m := reKeyContains.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "CONTAINS", Target: "key", Pattern: m[1]}, nil
	}

	reKeyNotContains := regexp.MustCompile(`(?i)^key\s+NOT_CONTAINS\s+['"](.+?)['"]$`)
	if m := reKeyNotContains.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "NOT_CONTAINS", Target: "key", Pattern: m[1]}, nil
	}

	reKeyMatches := regexp.MustCompile(`(?i)^key\s+MATCHES\s/(.+?)/$`)
	if m := reKeyMatches.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "MATCHES", Target: "key", Pattern: m[1]}, nil
	}

	// Kafka targets: value
	reValueContains := regexp.MustCompile(`(?i)^value\s+CONTAINS\s+['"](.+?)['"]$`)
	if m := reValueContains.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "CONTAINS", Target: "value", Pattern: m[1]}, nil
	}

	reValueNotContains := regexp.MustCompile(`(?i)^value\s+NOT_CONTAINS\s+['"](.+?)['"]$`)
	if m := reValueNotContains.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "NOT_CONTAINS", Target: "value", Pattern: m[1]}, nil
	}

	reValueMatches := regexp.MustCompile(`(?i)^value\s+MATCHES\s/(.+?)/$`)
	if m := reValueMatches.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "MATCHES", Target: "value", Pattern: m[1]}, nil
	}

	// Kafka targets: headers.NAME
	reKafkaHeadersContains := regexp.MustCompile(`(?i)^headers\.([\w-]+)\s+CONTAINS\s+['"](.+?)['"]$`)
	if m := reKafkaHeadersContains.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "CONTAINS", Target: "headers." + m[1], Pattern: m[2]}, nil
	}

	reKafkaHeadersNotContains := regexp.MustCompile(`(?i)^headers\.([\w-]+)\s+NOT_CONTAINS\s+['"](.+?)['"]$`)
	if m := reKafkaHeadersNotContains.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "NOT_CONTAINS", Target: "headers." + m[1], Pattern: m[2]}, nil
	}

	reKafkaHeadersMatches := regexp.MustCompile(`(?i)^headers\.([\w-]+)\s+MATCHES\s/(.+?)/$`)
	if m := reKafkaHeadersMatches.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "MATCHES", Target: "headers." + m[1], Pattern: m[2]}, nil
	}

	// gRPC targets: request_body
	reRequestBodyContains := regexp.MustCompile(`(?i)^request_body\s+CONTAINS\s+['"](.+?)['"]$`)
	if m := reRequestBodyContains.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "CONTAINS", Target: "request_body", Pattern: m[1]}, nil
	}

	reRequestBodyNotContains := regexp.MustCompile(`(?i)^request_body\s+NOT_CONTAINS\s+['"](.+?)['"]$`)
	if m := reRequestBodyNotContains.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "NOT_CONTAINS", Target: "request_body", Pattern: m[1]}, nil
	}

	reRequestBodyMatches := regexp.MustCompile(`(?i)^request_body\s+MATCHES\s/(.+?)/$`)
	if m := reRequestBodyMatches.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "MATCHES", Target: "request_body", Pattern: m[1]}, nil
	}

	// gRPC targets: metadata.NAME
	reMetadataContains := regexp.MustCompile(`(?i)^metadata\.([\w-]+)\s+CONTAINS\s+['"](.+?)['"]$`)
	if m := reMetadataContains.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "CONTAINS", Target: "metadata." + m[1], Pattern: m[2]}, nil
	}

	reMetadataNotContains := regexp.MustCompile(`(?i)^metadata\.([\w-]+)\s+NOT_CONTAINS\s+['"](.+?)['"]$`)
	if m := reMetadataNotContains.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "NOT_CONTAINS", Target: "metadata." + m[1], Pattern: m[2]}, nil
	}

	reMetadataMatches := regexp.MustCompile(`(?i)^metadata\.([\w-]+)\s+MATCHES\s/(.+?)/$`)
	if m := reMetadataMatches.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "MATCHES", Target: "metadata." + m[1], Pattern: m[2]}, nil
	}

	// Redis targets: command
	reCommandContains := regexp.MustCompile(`(?i)^command\s+CONTAINS\s+['"](.+?)['"]$`)
	if m := reCommandContains.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "CONTAINS", Target: "command", Pattern: m[1]}, nil
	}

	reCommandNotContains := regexp.MustCompile(`(?i)^command\s+NOT_CONTAINS\s+['"](.+?)['"]$`)
	if m := reCommandNotContains.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "NOT_CONTAINS", Target: "command", Pattern: m[1]}, nil
	}

	reCommandMatches := regexp.MustCompile(`(?i)^command\s+MATCHES\s/(.+?)/$`)
	if m := reCommandMatches.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "MATCHES", Target: "command", Pattern: m[1]}, nil
	}

	return nil, fmt.Errorf("Invalid VERIFY format at line %d: %s", line, value)
}
