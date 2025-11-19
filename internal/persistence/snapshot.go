package persistence

import (
)



func (aof *AOF) createSnapshot () (*AOF, error) {
	tempName := aof.fileName + "_temp"
	tempFile, err := NewAOF(tempName, aof.policy)


}

