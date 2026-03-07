package dsl

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

type TokenType string

const (
	TokenTest          TokenType = "TEST"
	TokenReceive       TokenType = "RECEIVE"
	TokenExpect        TokenType = "EXPECT"
	TokenExpectNot     TokenType = "EXPECT_NOT"
	TokenRespond       TokenType = "RESPOND"
	TokenWith          TokenType = "WITH"
	TokenReturns       TokenType = "RETURNS"
	TokenVerify        TokenType = "VERIFY"
	TokenNoise         TokenType = "NOISE"
	TokenHeaders       TokenType = "HEADERS"
	TokenUsingSql      TokenType = "USING_SQL"
	TokenNoTransaction TokenType = "NO_TRANSACTION"
	TokenSqlBlock      TokenType = "SQL_BLOCK"
	TokenEOF           TokenType = "EOF"
)

type Token struct {
	Type    TokenType
	Literal string
	Line    int
}

type LexerError struct {
	Message string
	Line    int
}

func (e *LexerError) Error() string {
	return fmt.Sprintf("Lexer error at line %d: %s", e.Line, e.Message)
}

func LexFile(filePath string) ([]Token, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var tokens []Token
	scanner := bufio.NewScanner(file)
	lineNum := 0

	reTest := regexp.MustCompile(`(?i)^TEST\s+(.+)$`)
	reReceive := regexp.MustCompile(`(?i)^RECEIVE\s+(.+)$`)
	reExpectNot := regexp.MustCompile(`(?i)^EXPECT\s+NOT\s+(.+)$`)
	reExpect := regexp.MustCompile(`(?i)^EXPECT\s+(.+)$`)
	reRespond := regexp.MustCompile(`(?i)^RESPOND\s+(.+)$`)
	reWith := regexp.MustCompile(`(?i)^WITH\s+\{\{(.+)\}\}$`)
	reReturns := regexp.MustCompile(`(?i)^RETURNS\s+(.+)$`)
	reVerify := regexp.MustCompile(`(?i)^VERIFY\s+(.+)$`)

	inSqlBlock := false
	var sqlBuffer strings.Builder

	inIndentedBlock := false
	var indentedBuffer strings.Builder
	var currentBlockType TokenType

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmedLine := strings.TrimSpace(line)

		if trimmedLine == "" || strings.HasPrefix(trimmedLine, "#") {
			continue
		}

		// Handle multi-line SQL block
		if inSqlBlock {
			if strings.Contains(line, `"""`) {
				sqlPart := strings.Split(line, `"""`)[0]
				sqlBuffer.WriteString(sqlPart)
				tokens = append(tokens, Token{
					Type:    TokenSqlBlock,
					Literal: strings.TrimSpace(sqlBuffer.String()),
					Line:    lineNum,
				})
				inSqlBlock = false
				sqlBuffer.Reset()
				continue
			}
			sqlBuffer.WriteString(line + "\n")
			continue
		}

		if strings.Contains(line, `USING_SQL """`) {
			inSqlBlock = true
			tokens = append(tokens, Token{Type: TokenUsingSql, Line: lineNum})
			continue
		}

		// Handle indented blocks for HEADERS and NOISE
		if inIndentedBlock {
			if strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t") {
				indentedBuffer.WriteString(trimmedLine + "\n")
				continue
			} else {
				// End of indented block
				tokens = append(tokens, Token{
					Type:    currentBlockType,
					Literal: strings.TrimSpace(indentedBuffer.String()),
					Line:    lineNum - 1,
				})
				inIndentedBlock = false
				indentedBuffer.Reset()
				// Don't continue, process current line
			}
		}

		if strings.ToUpper(trimmedLine) == "HEADERS" {
			inIndentedBlock = true
			currentBlockType = TokenHeaders
			continue
		}

		if strings.ToUpper(trimmedLine) == "NOISE" {
			inIndentedBlock = true
			currentBlockType = TokenNoise
			continue
		}

		if strings.ToUpper(trimmedLine) == "NO TRANSACTION" {
			tokens = append(tokens, Token{Type: TokenNoTransaction, Line: lineNum})
			continue
		}

		if m := reTest.FindStringSubmatch(trimmedLine); m != nil {
			tokens = append(tokens, Token{Type: TokenTest, Literal: m[1], Line: lineNum})
		} else if m := reReceive.FindStringSubmatch(trimmedLine); m != nil {
			tokens = append(tokens, Token{Type: TokenReceive, Literal: m[1], Line: lineNum})
		} else if m := reExpectNot.FindStringSubmatch(trimmedLine); m != nil {
			tokens = append(tokens, Token{Type: TokenExpectNot, Literal: m[1], Line: lineNum})
		} else if m := reExpect.FindStringSubmatch(trimmedLine); m != nil {
			tokens = append(tokens, Token{Type: TokenExpect, Literal: m[1], Line: lineNum})
		} else if m := reRespond.FindStringSubmatch(trimmedLine); m != nil {
			tokens = append(tokens, Token{Type: TokenRespond, Literal: m[1], Line: lineNum})
		} else if m := reWith.FindStringSubmatch(trimmedLine); m != nil {
			tokens = append(tokens, Token{Type: TokenWith, Literal: m[1], Line: lineNum})
		} else if m := reReturns.FindStringSubmatch(trimmedLine); m != nil {
			tokens = append(tokens, Token{Type: TokenReturns, Literal: m[1], Line: lineNum})
		} else if m := reVerify.FindStringSubmatch(trimmedLine); m != nil {
			tokens = append(tokens, Token{Type: TokenVerify, Literal: m[1], Line: lineNum})
		} else {
			// Unknown line
			return nil, &LexerError{Message: fmt.Sprintf("Unexpected line: %s", trimmedLine), Line: lineNum}
		}
	}

	// Flush any remaining indented block
	if inIndentedBlock {
		tokens = append(tokens, Token{
			Type:    currentBlockType,
			Literal: strings.TrimSpace(indentedBuffer.String()),
			Line:    lineNum,
		})
	}

	tokens = append(tokens, Token{Type: TokenEOF, Line: lineNum})
	return tokens, nil
}
