//go:build !windows

package opencodev2

import (
	"context"
	"net"
	"path/filepath"
)

func reportBridgeAddress(dataDir, key string) string {
	return filepath.Join(dataDir, "run", "oc2-"+key[:16]+".sock")
}

func dialReportBridge(ctx context.Context, address string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "unix", address)
}
