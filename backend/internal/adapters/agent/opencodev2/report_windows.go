//go:build windows

package opencodev2

import (
	"context"
	"net"

	"github.com/Microsoft/go-winio"
)

func reportBridgeAddress(_, key string) string {
	return `\\.\pipe\ao-oc2-report-` + key
}

func dialReportBridge(ctx context.Context, address string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, address)
}
