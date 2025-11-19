package persistence

import (
	"os"
)

func newTempFile (fileName string, policy SyncPolicy) (*AOF, error) {
	tempFile, err := os.OpenFile(fileName, os.O_CREATE | os.O_WRONLY | os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	aof := &AOF {
		file: tempFile,
		fileName: fileName,
		policy: policy,
		done: make(chan struct{}),
	}

	if policy == SyncEverySecond {
		go aof.periodicSync()
	}

	return aof, nil
}

