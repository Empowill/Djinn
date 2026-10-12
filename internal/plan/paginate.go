package plan

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"

	"connectrpc.com/connect"
)

const (
	// DefaultPageSize is the default number of items returned when page_size is not specified or <= 0.
	DefaultPageSize = 50
	// MaxPageSize is the maximum allowed page size.
	MaxPageSize = 200
)

// Paginate slices items according to pageSize and pageToken.
// If pageSize <= 0, DefaultPageSize is used. If pageSize > MaxPageSize, MaxPageSize is used.
// If pageToken is empty, pagination starts at the beginning (offset 0).
// If pageToken is invalid, a connect error with CodeInvalidArgument is returned.
// Returns the sliced items, next_page_token (or "" if on the last page), total items count, and an error if any.
func Paginate[T any](items []T, pageSize int32, pageToken string) ([]T, string, int32, error) {
	total := int32(len(items))
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	} else if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}

	offset := 0
	if pageToken != "" {
		raw, err := base64.RawURLEncoding.DecodeString(pageToken)
		if err != nil {
			raw, err = base64.StdEncoding.DecodeString(pageToken)
		}
		if err != nil {
			return nil, "", 0, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid page token: %w", err))
		}
		n, err := strconv.Atoi(string(raw))
		if err != nil || n < 0 {
			return nil, "", 0, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid page token"))
		}
		offset = n
	}

	if offset >= len(items) {
		return []T{}, "", total, nil
	}

	end := offset + int(pageSize)
	var nextToken string
	if end < len(items) {
		nextToken = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	} else {
		end = len(items)
	}

	return items[offset:end], nextToken, total, nil
}
