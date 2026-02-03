package terminal

import "strings"

type oscTitleState int

const (
	oscStateText oscTitleState = iota
	oscStateEsc
	oscStateOSC
	oscStateParam
	oscStateTitle
	oscStateTitleEsc
)

type oscTitleParser struct {
	state   oscTitleState
	param   int
	capture bool
	buf     []byte
	maxSize int
}

func newOSCTitleParser() *oscTitleParser {
	return &oscTitleParser{
		state:   oscStateText,
		maxSize: 8192,
	}
}

func (p *oscTitleParser) Feed(data []byte) []string {
	if len(data) == 0 {
		return nil
	}

	var titles []string
	for _, b := range data {
		switch p.state {
		case oscStateText:
			if b == 0x1b {
				p.state = oscStateEsc
			}
		case oscStateEsc:
			if b == ']' {
				p.state = oscStateOSC
				p.param = 0
				p.capture = false
				p.buf = p.buf[:0]
				break
			}
			p.state = oscStateText
			if b == 0x1b {
				p.state = oscStateEsc
			}
		case oscStateOSC:
			if b >= '0' && b <= '9' {
				p.param = int(b - '0')
				p.state = oscStateParam
				break
			}
			if b == ';' {
				p.capture = p.param == 0 || p.param == 2
				p.buf = p.buf[:0]
				p.state = oscStateTitle
				break
			}
			p.state = oscStateText
		case oscStateParam:
			if b >= '0' && b <= '9' {
				p.param = p.param*10 + int(b-'0')
				break
			}
			if b == ';' {
				p.capture = p.param == 0 || p.param == 2
				p.buf = p.buf[:0]
				p.state = oscStateTitle
				break
			}
			p.state = oscStateText
		case oscStateTitle:
			if b == 0x07 {
				if p.capture && len(p.buf) > 0 {
					titles = append(titles, string(p.buf))
				}
				p.buf = p.buf[:0]
				p.state = oscStateText
				break
			}
			if b == 0x1b {
				p.state = oscStateTitleEsc
				break
			}
			if p.capture && len(p.buf) < p.maxSize {
				p.buf = append(p.buf, b)
			}
		case oscStateTitleEsc:
			if b == '\\' {
				if p.capture && len(p.buf) > 0 {
					titles = append(titles, string(p.buf))
				}
				p.buf = p.buf[:0]
				p.state = oscStateText
				break
			}
			if p.capture && len(p.buf) < p.maxSize {
				p.buf = append(p.buf, 0x1b)
				if len(p.buf) < p.maxSize {
					p.buf = append(p.buf, b)
				}
			}
			p.state = oscStateTitle
		default:
			p.state = oscStateText
		}
	}
	return titles
}

func parseAlicesMirrorTitle(title string) (cwd string, proc string, ok bool) {
	first := strings.Index(title, "|")
	if first <= 0 {
		return "", "", false
	}
	prefix := title[:first]
	if !strings.HasPrefix(prefix, "alices-mirror") {
		return "", "", false
	}
	rest := title[first+1:]
	second := strings.Index(rest, "|")
	if second < 0 {
		return "", "", false
	}
	cwd = strings.TrimSpace(rest[:second])
	proc = strings.TrimSpace(rest[second+1:])
	if cwd == "" && proc == "" {
		return "", "", false
	}
	return cwd, proc, true
}
