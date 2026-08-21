package imapc

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

type Client struct {
	conn        *tls.Conn
	br          *bufio.Reader
	mu          sync.Mutex
	tag         uint64
	literalPlus bool
	idle        bool
	uidNext     uint32
	exists      uint32
}

type Message struct {
	UID  uint32
	Body []byte
}

type DialOpts struct {
	DialHost       string
	SNI            string
	Port           int
	Timeout        time.Duration
	InterfaceIndex uint32
	LocalIP        net.IP
}

func Dial(ctx context.Context, o DialOpts) (*Client, error) {
	if o.Port == 0 {
		o.Port = 993
	}
	if o.Timeout == 0 {
		o.Timeout = 20 * time.Second
	}
	if o.SNI == "" {
		o.SNI = o.DialHost
	}
	d := &net.Dialer{
		Timeout: o.Timeout,
		Control: interfaceControl(o.InterfaceIndex),
	}
	if ip := o.LocalIP.To4(); ip != nil {
		d.LocalAddr = &net.TCPAddr{IP: ip}
	}
	addr := net.JoinHostPort(o.DialHost, strconv.Itoa(o.Port))
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("imap dial %s: %w", addr, err)
	}
	tlsConn := tls.Client(raw, &tls.Config{
		ServerName: o.SNI,
		MinVersion: tls.VersionTLS12,
	})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("imap tls %s sni=%s: %w", addr, o.SNI, err)
	}
	c := &Client{conn: tlsConn, br: bufio.NewReader(tlsConn)}
	if _, err := c.readLine(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("imap greeting: %w", err)
	}
	return c, nil
}

func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) Login(ctx context.Context, user, pass string) error {
	caps, err := c.Capability(ctx)
	if err != nil {
		return err
	}
	c.literalPlus = strings.Contains(caps, "LITERAL+")
	_, err = c.exec(ctx, "LOGIN %s %s", quote(user), quote(pass))
	return err
}

func (c *Client) Capability(ctx context.Context) (string, error) {
	lines, err := c.exec(ctx, "CAPABILITY")
	if err != nil {
		return "", err
	}
	for _, ln := range lines {
		if strings.HasPrefix(strings.ToUpper(ln), "* CAPABILITY") {
			return strings.ToUpper(ln), nil
		}
	}
	return "", nil
}

func (c *Client) Create(ctx context.Context, mailbox string) error {
	_, err := c.exec(ctx, "CREATE %s", quote(mailbox))
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "already") {
		return err
	}
	return nil
}

func (c *Client) Select(ctx context.Context, mailbox string) error {
	lines, err := c.exec(ctx, "SELECT %s", quote(mailbox))
	if err != nil {
		return err
	}
	for _, ln := range lines {
		u := strings.ToUpper(ln)
		if strings.HasPrefix(u, "* ") && strings.HasSuffix(strings.TrimSpace(u), " EXISTS") {
			fields := strings.Fields(u)
			if len(fields) >= 3 {
				if n, err := strconv.Atoi(fields[1]); err == nil && n >= 0 {
					c.exists = uint32(n)
				}
			}
		}
		if i := strings.Index(u, "[UIDNEXT "); i >= 0 {
			rest := u[i+len("[UIDNEXT "):]
			rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
			n := 0
			for _, ch := range rest {
				if ch < '0' || ch > '9' {
					break
				}
				n = n*10 + int(ch-'0')
			}
			if n > 0 {
				c.uidNext = uint32(n)
			}
		}
	}
	return nil
}

func (c *Client) UIDNext() uint32 { return c.uidNext }

func (c *Client) Exists() uint32 { return c.exists }

func (c *Client) Noop(ctx context.Context) error {
	_, err := c.exec(ctx, "NOOP")
	return err
}

func (c *Client) Logout(ctx context.Context) {
	_, _ = c.exec(ctx, "LOGOUT")
}

func (c *Client) Append(ctx context.Context, mailbox string, body []byte) error {
	if c == nil {
		return fmt.Errorf("imap client is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.conn.SetDeadline(deadline(ctx, 60*time.Second)); err != nil {
		return err
	}
	tag := c.nextTag()
	if c.literalPlus {
		if err := c.writef("%s APPEND %s (\\Draft \\Seen) {%d+}\r\n", tag, quote(mailbox), len(body)); err != nil {
			return err
		}
		if _, err := c.conn.Write(body); err != nil {
			return err
		}
		if _, err := c.conn.Write([]byte("\r\n")); err != nil {
			return err
		}
		_, err := c.readUntilTag(tag)
		return err
	}
	if err := c.writef("%s APPEND %s (\\Draft \\Seen) {%d}\r\n", tag, quote(mailbox), len(body)); err != nil {
		return err
	}
	line, err := c.readLine()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(line, "+") {
		return fmt.Errorf("imap append cont: %s", line)
	}
	if _, err := c.conn.Write(body); err != nil {
		return err
	}
	if _, err := c.conn.Write([]byte("\r\n")); err != nil {
		return err
	}
	_, err = c.readUntilTag(tag)
	return err
}

func (c *Client) IdleUntilExists(ctx context.Context, maxIdle time.Duration) error {
	if c == nil {
		return fmt.Errorf("imap client is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.conn.SetWriteDeadline(deadline(ctx, 15*time.Second)); err != nil {
		return err
	}
	tag := c.nextTag()
	if err := c.writef("%s IDLE\r\n", tag); err != nil {
		return err
	}
	c.idle = true
	pendingChange := false
	for {
		line, err := c.readLine()
		if err != nil {
			c.idle = false
			return err
		}
		up := strings.ToUpper(line)
		if strings.HasPrefix(line, "+") {
			break
		}
		if strings.Contains(up, "EXISTS") || strings.Contains(up, "RECENT") || strings.Contains(up, "FETCH") {
			pendingChange = true
			continue
		}
		if strings.HasPrefix(line, tag+" ") {
			c.idle = false
			return fmt.Errorf("imap idle start: %s", line)
		}
	}
	if pendingChange {
		return c.abortIdle(tag)
	}
	if maxIdle <= 0 {
		maxIdle = 45 * time.Second
	}
	started := time.Now()
	for {
		if err := c.conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			c.abortIdle(tag)
			return err
		}
		line, err := c.readLine()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if ctx.Err() != nil {
					return c.abortIdle(tag)
				}
				if time.Since(started) >= maxIdle {
					return c.abortIdle(tag)
				}
				continue
			}
			c.idle = false
			return err
		}
		up := strings.ToUpper(line)
		if strings.Contains(up, "EXISTS") || strings.Contains(up, "RECENT") || strings.Contains(up, "FETCH") {
			return c.abortIdle(tag)
		}
		if strings.HasPrefix(line, tag+" ") {
			c.idle = false
			if strings.Contains(up, " OK") {
				return ctx.Err()
			}
			return fmt.Errorf("imap idle end: %s", line)
		}
		if ctx.Err() != nil {
			return c.abortIdle(tag)
		}
	}
}

func (c *Client) abortIdle(tag string) error {
	_ = c.conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := c.writef("DONE\r\n"); err != nil {
		c.idle = false
		return err
	}
	_, err := c.readUntilTag(tag)
	c.idle = false
	return err
}

func (c *Client) UIDFetchText(ctx context.Context, fromUID uint32) ([]Message, error) {
	if fromUID == 0 {
		fromUID = 1
	}
	lines, err := c.exec(ctx, "UID FETCH %d:* (UID BODY.PEEK[TEXT])", fromUID)
	if err != nil {
		return nil, err
	}
	blob := strings.Join(lines, "\n")
	return parseFetch(blob), nil
}

func (c *Client) UIDSearchOlder(ctx context.Context, seconds int) ([]uint32, error) {
	if seconds < 1 {
		seconds = 1
	}
	lines, err := c.exec(ctx, "UID SEARCH OLDER %d", seconds)
	if err != nil {
		lines, err = c.exec(ctx, "UID SEARCH UNDELETED OLDER %d", seconds)
		if err != nil {
			return nil, err
		}
	}
	return parseSearch(lines), nil
}

func (c *Client) UIDSearchDeleted(ctx context.Context) ([]uint32, error) {
	lines, err := c.exec(ctx, "UID SEARCH DELETED")
	if err != nil {
		return nil, err
	}
	return parseSearch(lines), nil
}

func (c *Client) UIDSearchAll(ctx context.Context) ([]uint32, error) {
	lines, err := c.exec(ctx, "UID SEARCH ALL")
	if err != nil {
		return nil, err
	}
	return parseSearch(lines), nil
}

func (c *Client) UIDDelete(ctx context.Context, uids []uint32) error {
	if len(uids) == 0 {
		return nil
	}
	dctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	for _, chunk := range chunkUIDs(uids, 400) {
		set := compactUIDs(chunk)
		if set == "" {
			continue
		}
		if _, err := c.exec(dctx, "UID STORE %s +FLAGS.SILENT (\\Deleted)", set); err != nil {
			return err
		}
	}
	_, err := c.exec(dctx, "EXPUNGE")
	return err
}

func (c *Client) exec(ctx context.Context, format string, args ...any) ([]string, error) {
	if c == nil {
		return nil, fmt.Errorf("imap client is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.conn.SetDeadline(deadline(ctx, 45*time.Second)); err != nil {
		return nil, err
	}
	tag := c.nextTag()
	if err := c.writef("%s "+format+"\r\n", append([]any{tag}, args...)...); err != nil {
		return nil, err
	}
	return c.readUntilTag(tag)
}

func (c *Client) nextTag() string {
	c.tag++
	return fmt.Sprintf("A%04d", c.tag)
}

func (c *Client) writef(format string, args ...any) error {
	s := fmt.Sprintf(format, args...)
	_, err := io.WriteString(c.conn, s)
	return err
}

func (c *Client) readUntilTag(tag string) ([]string, error) {
	var lines []string
	for {
		line, err := c.readLine()
		if err != nil {
			return lines, err
		}
		lines = append(lines, line)
		if strings.HasPrefix(line, tag+" ") {
			rest := strings.ToUpper(strings.TrimSpace(line[len(tag):]))
			if strings.HasPrefix(rest, "OK") {
				return lines, nil
			}
			return lines, fmt.Errorf("imap %s", strings.TrimSpace(line))
		}
	}
}

func (c *Client) readLine() (string, error) {
	var b strings.Builder
	for {
		part, err := c.br.ReadString('\n')
		if err != nil {
			return "", err
		}
		b.WriteString(strings.TrimRight(part, "\r\n"))
		s := b.String()
		n, plus, ok := trailingLiteral(s)
		if !ok {
			return s, nil
		}
		_ = plus
		buf := make([]byte, n)
		if _, err := io.ReadFull(c.br, buf); err != nil {
			return "", err
		}
		b.WriteByte('\n')
		b.Write(buf)
	}
}

func trailingLiteral(line string) (n int, plus bool, ok bool) {
	i := strings.LastIndex(line, "{")
	if i < 0 || !strings.HasSuffix(line, "}") {
		return 0, false, false
	}
	inner := line[i+1 : len(line)-1]
	if strings.HasSuffix(inner, "+") {
		plus = true
		inner = inner[:len(inner)-1]
	}
	if inner == "" {
		return 0, plus, false
	}
	for _, ch := range inner {
		if ch < '0' || ch > '9' {
			return 0, plus, false
		}
	}
	v, err := strconv.Atoi(inner)
	if err != nil || v < 0 || v > 8<<20 {
		return 0, plus, false
	}
	return v, plus, true
}

func parseFetch(blob string) []Message {
	var out []Message
	idx := 0
	for {
		i := strings.Index(strings.ToUpper(blob[idx:]), "* ")
		if i < 0 {
			break
		}
		i += idx
		chunkStart := i
		rest := blob[i:]
		uid := uint32(0)
		if j := strings.Index(strings.ToUpper(rest), "UID "); j >= 0 {
			p := j + 4
			n := 0
			for p < len(rest) && rest[p] >= '0' && rest[p] <= '9' {
				n = n*10 + int(rest[p]-'0')
				p++
			}
			uid = uint32(n)
		}
		body := extractText(rest)
		if uid != 0 && len(body) > 0 {
			out = append(out, Message{UID: uid, Body: body})
		}
		next := strings.Index(blob[chunkStart+2:], "\n*")
		if next < 0 {
			break
		}
		idx = chunkStart + 2 + next + 1
	}
	return out
}

func extractText(rest string) []byte {
	up := strings.ToUpper(rest)
	key := "BODY[TEXT]"
	j := strings.Index(up, key)
	if j < 0 {
		key = "BODY[]"
		j = strings.Index(up, key)
		if j < 0 {
			return nil
		}
	}
	s := strings.TrimSpace(rest[j+len(key):])
	if strings.HasPrefix(s, "NIL") {
		return nil
	}
	if strings.HasPrefix(s, "{") {
		end := strings.Index(s, "}")
		if end < 0 {
			return nil
		}
		inner := strings.TrimSuffix(s[1:end], "+")
		n, err := strconv.Atoi(inner)
		if err != nil || n < 0 {
			return nil
		}
		data := s[end+1:]
		if len(data) > 0 && data[0] == '\n' {
			data = data[1:]
		}
		if len(data) < n {
			return []byte(data)
		}
		return []byte(data[:n])
	}
	if strings.HasPrefix(s, "\"") {
		s = s[1:]
		k := strings.IndexByte(s, '"')
		if k < 0 {
			return []byte(s)
		}
		return []byte(s[:k])
	}
	return nil
}

func parseSearch(lines []string) []uint32 {
	var out []uint32
	for _, ln := range lines {
		up := strings.ToUpper(strings.TrimSpace(ln))
		if strings.HasPrefix(up, "* SEARCH") {
			i := strings.Index(strings.ToUpper(ln), "* SEARCH")
			rest := strings.TrimSpace(ln[i+len("* SEARCH"):])
			out = append(out, parseUIDList(rest)...)
			continue
		}
		if strings.Contains(up, "ESEARCH") && strings.Contains(up, " ALL ") {
			i := strings.Index(up, " ALL ")
			if i < 0 {
				continue
			}
			rest := strings.TrimSpace(ln[i+len(" ALL "):])
			if sp := strings.IndexByte(rest, ' '); sp >= 0 {
				rest = rest[:sp]
			}
			if rp := strings.IndexByte(rest, ')'); rp >= 0 {
				rest = rest[:rp]
			}
			out = append(out, expandSeqSet(rest)...)
		}
	}
	return uniqUIDs(out)
}

func parseUIDList(s string) []uint32 {
	var out []uint32
	for _, p := range strings.Fields(s) {
		out = append(out, expandSeqSet(p)...)
	}
	return out
}

func expandSeqSet(set string) []uint32 {
	var out []uint32
	if set == "" || set == "NIL" {
		return out
	}
	for _, part := range strings.Split(set, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		a, b, ok := strings.Cut(part, ":")
		if !ok {
			n, err := strconv.ParseUint(part, 10, 32)
			if err == nil && n > 0 {
				out = append(out, uint32(n))
			}
			continue
		}
		if a == "*" || b == "*" {
			continue
		}
		lo, err1 := strconv.ParseUint(a, 10, 32)
		hi, err2 := strconv.ParseUint(b, 10, 32)
		if err1 != nil || err2 != nil || lo == 0 || hi == 0 {
			continue
		}
		if lo > hi {
			lo, hi = hi, lo
		}
		if hi-lo > 500000 {
			continue
		}
		for n := lo; n <= hi; n++ {
			out = append(out, uint32(n))
		}
	}
	return out
}

func compactUIDs(uids []uint32) string {
	uids = uniqUIDs(uids)
	if len(uids) == 0 {
		return ""
	}
	var parts []string
	start, prev := uids[0], uids[0]
	flush := func() {
		if start == prev {
			parts = append(parts, strconv.FormatUint(uint64(start), 10))
		} else {
			parts = append(parts, fmt.Sprintf("%d:%d", start, prev))
		}
	}
	for _, u := range uids[1:] {
		if u == prev+1 {
			prev = u
			continue
		}
		flush()
		start, prev = u, u
	}
	flush()
	return strings.Join(parts, ",")
}

func uniqUIDs(uids []uint32) []uint32 {
	if len(uids) == 0 {
		return uids
	}
	seen := make(map[uint32]struct{}, len(uids))
	out := make([]uint32, 0, len(uids))
	for _, u := range uids {
		if u == 0 {
			continue
		}
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func chunkUIDs(uids []uint32, n int) [][]uint32 {
	uids = uniqUIDs(uids)
	if n <= 0 {
		n = 400
	}
	var out [][]uint32
	for len(uids) > 0 {
		if len(uids) < n {
			n = len(uids)
		}
		out = append(out, uids[:n])
		uids = uids[n:]
	}
	return out
}

func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		if r == '\\' || r == '"' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

func deadline(ctx context.Context, d time.Duration) time.Time {
	if dl, ok := ctx.Deadline(); ok {
		return dl
	}
	return time.Now().Add(d)
}
