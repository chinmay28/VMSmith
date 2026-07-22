package api

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Minimal Guacamole protocol codec (roadmap 5.6.13). The daemon acts as a
// Guacamole protocol client toward guacd: it performs the connection
// handshake (select rdp → args → size/audio/video/image → connect → ready)
// and then relays opaque instructions between guacd and the browser's
// guacamole-common-js client.
//
// Wire format: an instruction is a comma-separated list of
// length-prefixed elements terminated by a semicolon, e.g.
// `6.select,3.rdp;`. Lengths count Unicode code points, not bytes.

// guacInstruction is a parsed opcode + args.
type guacInstruction struct {
	Opcode string
	Args   []string
}

// encodeGuacInstruction renders an instruction in wire format.
func encodeGuacInstruction(opcode string, args ...string) string {
	var sb strings.Builder
	writeElem := func(v string) {
		sb.WriteString(strconv.Itoa(len([]rune(v))))
		sb.WriteByte('.')
		sb.WriteString(v)
	}
	writeElem(opcode)
	for _, a := range args {
		sb.WriteByte(',')
		writeElem(a)
	}
	sb.WriteByte(';')
	return sb.String()
}

// guacReader incrementally decodes instructions from a stream.
type guacReader struct {
	r *bufio.Reader
}

func newGuacReader(r io.Reader) *guacReader {
	return &guacReader{r: bufio.NewReader(r)}
}

// readElement reads one `<len>.<value>` element and the delimiter that
// follows it, returning the value and whether the instruction ended (';').
func (g *guacReader) readElement() (string, bool, error) {
	lenStr, err := g.r.ReadString('.')
	if err != nil {
		return "", false, err
	}
	n, err := strconv.Atoi(strings.TrimSuffix(lenStr, "."))
	if err != nil || n < 0 {
		return "", false, fmt.Errorf("invalid guacamole element length %q", strings.TrimSuffix(lenStr, "."))
	}
	// Lengths are in runes.
	runes := make([]rune, n)
	for i := 0; i < n; i++ {
		r, _, err := g.r.ReadRune()
		if err != nil {
			return "", false, err
		}
		runes[i] = r
	}
	delim, _, err := g.r.ReadRune()
	if err != nil {
		return "", false, err
	}
	switch delim {
	case ',':
		return string(runes), false, nil
	case ';':
		return string(runes), true, nil
	default:
		return "", false, fmt.Errorf("invalid guacamole delimiter %q", delim)
	}
}

// ReadInstruction decodes the next full instruction.
func (g *guacReader) ReadInstruction() (*guacInstruction, error) {
	opcode, done, err := g.readElement()
	if err != nil {
		return nil, err
	}
	inst := &guacInstruction{Opcode: opcode}
	for !done {
		var arg string
		arg, done, err = g.readElement()
		if err != nil {
			return nil, err
		}
		inst.Args = append(inst.Args, arg)
	}
	return inst, nil
}

// guacdHandshakeRDP drives the guacd client handshake for an RDP
// connection to hostname:port. Returns the guacd reader (positioned after
// the `ready` instruction) and the ready instruction itself (whose first
// arg is the connection id) so the caller can forward it to the browser.
func guacdHandshakeRDP(conn io.ReadWriter, hostname string, port int, width, height int) (*guacReader, *guacInstruction, error) {
	if _, err := io.WriteString(conn, encodeGuacInstruction("select", "rdp")); err != nil {
		return nil, nil, fmt.Errorf("guacd select: %w", err)
	}

	reader := newGuacReader(conn)
	args, err := reader.ReadInstruction()
	if err != nil {
		return nil, nil, fmt.Errorf("guacd args: %w", err)
	}
	if args.Opcode != "args" {
		return nil, nil, fmt.Errorf("guacd handshake: expected args, got %q", args.Opcode)
	}

	if width <= 0 {
		width = 1024
	}
	if height <= 0 {
		height = 768
	}
	pre := encodeGuacInstruction("size", strconv.Itoa(width), strconv.Itoa(height), "96") +
		encodeGuacInstruction("audio") +
		encodeGuacInstruction("video") +
		encodeGuacInstruction("image", "image/png", "image/jpeg")
	if _, err := io.WriteString(conn, pre); err != nil {
		return nil, nil, fmt.Errorf("guacd size/audio/video/image: %w", err)
	}

	// The connect instruction supplies one value per parameter guacd
	// advertised in args (args[0] is the protocol version, answered
	// in-place when it is a VERSION_* token).
	values := make([]string, len(args.Args))
	for i, name := range args.Args {
		switch {
		case i == 0 && strings.HasPrefix(name, "VERSION_"):
			values[i] = name
		case name == "hostname":
			values[i] = hostname
		case name == "port":
			values[i] = strconv.Itoa(port)
		case name == "ignore-cert":
			values[i] = "true"
		case name == "security":
			// Let the server negotiate (NLA/TLS/RDP).
			values[i] = "any"
		case name == "resize-method":
			values[i] = "display-update"
		default:
			values[i] = ""
		}
	}
	if _, err := io.WriteString(conn, encodeGuacInstruction("connect", values...)); err != nil {
		return nil, nil, fmt.Errorf("guacd connect: %w", err)
	}

	ready, err := reader.ReadInstruction()
	if err != nil {
		return nil, nil, fmt.Errorf("guacd ready: %w", err)
	}
	if ready.Opcode != "ready" {
		return nil, nil, fmt.Errorf("guacd handshake: expected ready, got %q", ready.Opcode)
	}
	return reader, ready, nil
}
