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
	// open file for read/write, create if it doesn't exist
	file, err := os.OpenFile(fileName, os.O_CREATE | os.O_WRONLY | os.O_APPEND, 0644)
	if err != nil {
		fmt.Printf("Error opening file: %v", err)
	}

	return &AOF{file: file}, nil
}

func (aof *AOF) appendAOF (data string) error {
	aof.mu.Lock()
	defer aof.mu.Unlock()

	line := data + "\n"
	_, err := aof.file.Write([]byte(line))
	if err != nil {
		return err
	}

	// ensure durability
	err = aof.file.Sync()
	if err != nil {
		return fmt.Errorf("fsync failed: %v", err)
	}
	
	return nil
}