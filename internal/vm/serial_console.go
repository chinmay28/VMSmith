package vm

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/vmsmith/vmsmith/pkg/types"
	"libvirt.org/go/libvirt"
)

// OpenSerialConsole attaches to the domain's primary serial console via
// libvirt's virDomainOpenConsole and returns it as an io.ReadWriteCloser
// backed by a libvirt stream. The VM must be running — libvirt only
// allocates the console pty while the domain is alive.
func (m *LibvirtManager) OpenSerialConsole(ctx context.Context, id string) (io.ReadWriteCloser, error) {
	storedVM, err := m.store.GetVM(id)
	if err != nil {
		return nil, err
	}

	dom, err := m.conn.LookupDomainByName(storedVM.Name)
	if err != nil {
		return nil, fmt.Errorf("looking up domain: %w", err)
	}
	defer dom.Free()

	if state := domainStateToVMState(dom); state != types.VMStateRunning {
		return nil, types.NewAPIError("vm_not_running", "vm is not running; start it before opening a serial console")
	}

	stream, err := m.conn.NewStream(0)
	if err != nil {
		return nil, fmt.Errorf("creating libvirt stream: %w", err)
	}

	// Empty device name selects the domain's first console; FORCE detaches
	// a stale hung session (e.g. a previous daemon crash left the console
	// open) rather than failing with "console busy".
	if err := dom.OpenConsole("", stream, libvirt.DOMAIN_CONSOLE_FORCE); err != nil {
		_ = stream.Free()
		return nil, types.NewAPIError("console_unavailable", "domain has no attachable serial console")
	}

	return &libvirtStreamConsole{stream: stream}, nil
}

// libvirtStreamConsole adapts a *libvirt.Stream to io.ReadWriteCloser so
// the websocket proxy can treat serial consoles like any other stream.
type libvirtStreamConsole struct {
	stream    *libvirt.Stream
	closeOnce sync.Once
	closeErr  error
}

func (c *libvirtStreamConsole) Read(p []byte) (int, error) {
	n, err := c.stream.Recv(p)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

func (c *libvirtStreamConsole) Write(p []byte) (int, error) {
	written := 0
	for written < len(p) {
		n, err := c.stream.Send(p[written:])
		if err != nil {
			return written, err
		}
		if n <= 0 {
			return written, io.ErrShortWrite
		}
		written += n
	}
	return written, nil
}

func (c *libvirtStreamConsole) Close() error {
	c.closeOnce.Do(func() {
		// Abort tears down the server side of the stream; Free releases
		// the client object. Abort on an already-finished stream returns
		// an error we deliberately ignore.
		_ = c.stream.Abort()
		c.closeErr = c.stream.Free()
	})
	return c.closeErr
}
