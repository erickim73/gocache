package persistence

import (
	"sync"
	"os"
	"fmt"
)

type AOF struct {
	file * os.File
	mu sync.Mutex   // read write lock
}

func NewAOF (fileName string) (*AOF, error) {
	file, err := os.Create(fileName)
	if err != nil {
		fmt.Printf("Error creating file: %v", err)
	}

	return &AOF{file: file}, nil
}