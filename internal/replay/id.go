package replay

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ID is a structured reference resolved relative to the configured replay
// root. It never contains a filesystem path.
type ID struct {
	UserID    int
	ChartID   int
	Timestamp int64
}

func (id ID) String() string {
	return fmt.Sprintf("v1:%d:%d:%d", id.UserID, id.ChartID, id.Timestamp)
}

func ParseID(raw string) (ID, error) {
	parts := strings.Split(raw, ":")
	if len(parts) != 4 || parts[0] != "v1" {
		return ID{}, errors.New("invalid replay ID")
	}
	userID, err1 := strconv.Atoi(parts[1])
	chartID, err2 := strconv.Atoi(parts[2])
	timestamp, err3 := strconv.ParseInt(parts[3], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || userID < 0 || chartID <= 0 || timestamp <= 0 {
		return ID{}, errors.New("invalid replay ID")
	}
	return ID{UserID: userID, ChartID: chartID, Timestamp: timestamp}, nil
}

func (id ID) Path(baseDir string) (string, error) {
	if _, err := ParseID(id.String()); err != nil {
		return "", err
	}
	return FilePath(baseDir, id.UserID, id.ChartID, id.Timestamp), nil
}

func IDFromFile(info FileInfo) ID {
	return ID{UserID: info.UserID, ChartID: info.ChartID, Timestamp: info.Timestamp}
}
