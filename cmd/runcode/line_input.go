package main

import (
	"bufio"
	"context"
	"io"
	"sync"
)

type lineReader interface {
	ReadLine(ctx context.Context) (string, error)
}

type lineInput struct {
	lines chan lineInputResult
	done  chan struct{}
	once  sync.Once
}

type lineInputResult struct {
	text string
	err  error
}

func newLineInput(in io.Reader) *lineInput {
	input := &lineInput{lines: make(chan lineInputResult, 1), done: make(chan struct{})}
	go input.read(bufio.NewReader(in))
	return input
}

func (i *lineInput) ReadLine(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-i.done:
		return "", io.EOF
	case line, ok := <-i.lines:
		if !ok {
			return "", io.EOF
		}
		return line.text, line.err
	}
}

func (i *lineInput) Close() {
	i.once.Do(func() { close(i.done) })
}

func (i *lineInput) read(reader *bufio.Reader) {
	defer close(i.lines)
	for {
		line, err := reader.ReadString('\n')
		if line != "" && err == io.EOF {
			if !i.send(lineInputResult{text: line}) {
				return
			}
			return
		}
		if !i.send(lineInputResult{text: line, err: err}) {
			return
		}
		if err != nil {
			return
		}
	}
}

func (i *lineInput) send(result lineInputResult) bool {
	select {
	case <-i.done:
		return false
	case i.lines <- result:
		return true
	}
}
