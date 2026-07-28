//go:build !cgo

package collector

import "github.com/monitored-io/monitored/internal/domain"

func nvmlAvailable() bool { return false }

func collectNVML(string) (domain.IngestBatch, error) {
	return domain.IngestBatch{}, errNVMLUnavailable
}
