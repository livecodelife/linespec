package dsl

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/calebcowen/linespec/pkg/types"
)

type Parser struct {
	tokens []Token
	pos    int
}

func NewParser(tokens []Token) *Parser {
	return &Parser{tokens: tokens, pos: 0}
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
	m := reHttp.FindStringSubmatch(receiveToken.Literal)
	if m == nil {
		return nil, fmt.Errorf("Invalid RECEIVE format at line %d: %s", receiveToken.Line, receiveToken.Literal)
	}

	spec.Receive.Channel = types.HTTP
	spec.Receive.Method = strings.ToUpper(m[1])
	spec.Receive.Path = m[2]

	if p.peek().Type == TokenWith {
		spec.Receive.WithFile = p.consume().Literal
	}

	if p.peek().Type == TokenHeaders {
		headersToken := p.consume()
		spec.Receive.Headers = parseHeaders(headersToken.Literal)
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

	respondToken, err := p.expect(TokenRespond)
	if err != nil {
		return nil, err
	}

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

	return spec, nil
}

func (p *Parser) parseExpect() (*types.ExpectStatement, error) {
	token := p.consume()
	expect, err := parseExpectChannel(token.Literal, token.Line)
	if err != nil {
		return nil, err
	}

	if p.peek().Type == TokenUsingSql {
		p.consume() // TokenUsingSql
		sqlToken, err := p.expect(TokenSqlBlock)
		if err != nil {
			return nil, err
		}
		expect.SQL = sqlToken.Literal
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

	return nil, fmt.Errorf("Unrecognized EXPECT channel at line %d: %s", line, channelPart)
}

func parseVerifyRule(value string, line int) (*types.VerifyRule, error) {
	reContains := regexp.MustCompile(`(?i)^query\s+CONTAINS\s+['"](.+?)['"]$`)
	if m := reContains.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "CONTAINS", Pattern: m[1]}, nil
	}

	reNotContains := regexp.MustCompile(`(?i)^query\s+NOT_CONTAINS\s+['"](.+?)['"]$`)
	if m := reNotContains.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "NOT_CONTAINS", Pattern: m[1]}, nil
	}

	reMatches := regexp.MustCompile(`(?i)^query\s+MATCHES\s\/(.+?)\/$`)
	if m := reMatches.FindStringSubmatch(value); m != nil {
		return &types.VerifyRule{Type: "MATCHES", Pattern: m[1]}, nil
	}

	return nil, fmt.Errorf("Invalid VERIFY format at line %d: %s", line, value)
}
