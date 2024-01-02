package steamidutil

import (
	"fmt"
	"strconv"
)

const (
	accountTypeIdentifierIndividual = 76561197960265728
	accountTypeIdentifierGroup      = 103582791429521408
)

// TODO: support other account types

func IDToInt64(id string) (int64, error) {
	if len(id) < 11 {
		return 0, fmt.Errorf("ID is not the correct length")
	}

	y, err := strconv.ParseInt(id[8:9], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse account y: %w", err)
	}

	z, err := strconv.ParseInt(id[10:], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse account number: %w", err)
	}

	w := (z * 2) + accountTypeIdentifierIndividual + y

	return w, nil
}
