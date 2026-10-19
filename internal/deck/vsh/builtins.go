package vsh

import (
	"bufio"
	"cmp"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dev-dami/deck/internal/shell"
	"mvdan.cc/sh/v3/interp"
)

// builtin is a Go command. args do not include the name.
type builtin func(e *env, args []string) error

var goBuiltins = map[string]builtin{
	"cat": cat, "head": head, "tail": tail, "grep": grep, "sort": sortLines,
	"uniq": uniq, "wc": wc, "cut": cut, "tr": tr, "jq": jq,
}

const (
	maxInput = 64 << 20 // bytes a builtin reads from one input
	maxLine  = 1 << 20  // bytes in one line
)

var (
	errInputCap = errors.New("input is larger than 64 MiB")
	errStop     = errors.New("stop") // ends a lines loop early without an error
)

// capReader fails once more than left bytes were read.
type capReader struct {
	r    io.Reader
	left int64
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, errInputCap
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}

// open opens a file operand ("-" is stdin) with the input cap.
func (e *env) open(name string) (io.ReadCloser, error) {
	rc, err := e.fs.openRead(name, e.stdin)
	if err != nil {
		return nil, err
	}
	return struct {
		io.Reader
		io.Closer
	}{&capReader{r: rc, left: maxInput}, rc}, nil
}

// inputs calls f for each file operand, or for stdin when there is none.
func (e *env) inputs(files []string, f func(name string, r io.Reader) error) error {
	if len(files) == 0 {
		files = []string{"-"}
	}
	for _, name := range files {
		rc, err := e.open(name)
		if err != nil {
			return err
		}
		err = f(name, rc)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// lines calls f for each line of r without its newline. f may return
// errStop to end early.
func (e *env) lines(r io.Reader, f func(line string) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine)
	for sc.Scan() {
		if err := e.ctx.Err(); err != nil {
			return err
		}
		if err := f(sc.Text()); err != nil {
			if err == errStop {
				return nil
			}
			return err
		}
	}
	return sc.Err()
}

func (e *env) println(s string) error {
	_, err := io.WriteString(e.stdout, s+"\n")
	return err
}

// getopt splits args into options and operands. Letters in flags are
// switches. Letters in valued take a value ("-n 5" or "-n5"). Options end at
// the first operand or at "--".
func getopt(args []string, flags, valued string) (map[byte]string, []string, error) {
	opts := map[byte]string{}
	for len(args) > 0 {
		a := args[0]
		if a == "--" {
			return opts, args[1:], nil
		}
		if len(a) < 2 || a[0] != '-' {
			break
		}
		args = args[1:]
		for i := 1; i < len(a); i++ {
			c := a[i]
			switch {
			case strings.IndexByte(flags, c) >= 0:
				opts[c] = "1"
			case strings.IndexByte(valued, c) >= 0:
				v := a[i+1:]
				if v == "" {
					if len(args) == 0 {
						return nil, nil, fmt.Errorf("option -%c needs a value", c)
					}
					v, args = args[0], args[1:]
				}
				opts[c] = v
				i = len(a)
			default:
				return nil, nil, fmt.Errorf("unknown option -%c", c)
			}
		}
	}
	return opts, args, nil
}

// lineCount reads "-n N" or "-N" for head and tail.
func lineCount(args []string) (int, []string, error) {
	if len(args) > 0 && len(args[0]) > 1 && args[0][0] == '-' && only(args[0][1:], digits) {
		args = append([]string{"-n", args[0][1:]}, args[1:]...)
	}
	opts, files, err := getopt(args, "", "n")
	if err != nil {
		return 0, nil, err
	}
	n := 10
	if v, ok := opts['n']; ok {
		if n, err = strconv.Atoi(v); err != nil || n < 0 {
			return 0, nil, fmt.Errorf("bad line count %q", v)
		}
	}
	return n, files, nil
}

func cat(e *env, args []string) error {
	_, files, err := getopt(args, "", "")
	if err != nil {
		return err
	}
	return e.inputs(files, func(_ string, r io.Reader) error {
		_, err := io.Copy(e.stdout, r)
		return err
	})
}

func head(e *env, args []string) error {
	n, files, err := lineCount(args)
	if err != nil || n == 0 {
		return err
	}
	return e.inputs(files, func(_ string, r io.Reader) error {
		left := n
		return e.lines(r, func(l string) error {
			if err := e.println(l); err != nil {
				return err
			}
			if left--; left == 0 {
				return errStop
			}
			return nil
		})
	})
}

func tail(e *env, args []string) error {
	n, files, err := lineCount(args)
	if err != nil {
		return err
	}
	return e.inputs(files, func(_ string, r io.Reader) error {
		var last []string
		err := e.lines(r, func(l string) error {
			last = append(last, l)
			if len(last) > n {
				last = last[1:]
			}
			return nil
		})
		for _, l := range last {
			if err == nil {
				err = e.println(l)
			}
		}
		return err
	})
}

// grep supports -i -v -c -n -q -o -l -F -E -h -H and -e, with Go regexp syntax.
func grep(e *env, args []string) error {
	opts, files, err := getopt(args, "ivcnqolFEhH", "e")
	if err != nil {
		return err
	}
	pat, ok := opts['e']
	if !ok {
		if len(files) == 0 {
			return errors.New("no pattern")
		}
		pat, files = files[0], files[1:]
	}
	if opts['F'] != "" {
		pat = regexp.QuoteMeta(pat)
	}
	if opts['i'] != "" {
		pat = "(?i)" + pat
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return err
	}
	invert := opts['v'] != ""
	named := len(files) > 1 && opts['h'] == "" || opts['H'] != ""
	found := false
	err = e.inputs(files, func(name string, r io.Reader) error {
		prefix := ""
		if named {
			prefix = name + ":"
		}
		count, n := 0, 0
		err := e.lines(r, func(l string) error {
			n++
			if re.MatchString(l) == invert {
				return nil
			}
			found = true
			count++
			switch {
			case opts['q'] != "":
				return errStop
			case opts['l'] != "":
				if err := e.println(name); err != nil {
					return err
				}
				return errStop
			case opts['c'] != "":
				return nil
			}
			p := prefix
			if opts['n'] != "" {
				p += strconv.Itoa(n) + ":"
			}
			if opts['o'] == "" || invert {
				return e.println(p + l)
			}
			for _, m := range re.FindAllString(l, -1) {
				if err := e.println(p + m); err != nil {
					return err
				}
			}
			return nil
		})
		if err == nil && opts['c'] != "" {
			err = e.println(prefix + strconv.Itoa(count))
		}
		return err
	})
	if err != nil {
		return err
	}
	if !found {
		return interp.ExitStatus(1)
	}
	return nil
}

// sortLines supports -r -n -u, -k N (from field N to the end of the line)
// and -t SEP.
func sortLines(e *env, args []string) error {
	opts, files, err := getopt(args, "rnu", "kt")
	if err != nil {
		return err
	}
	field := 0
	if v, ok := opts['k']; ok {
		v, _, _ = strings.Cut(v, ",")
		if field, err = strconv.Atoi(v); err != nil || field < 1 {
			return fmt.Errorf("bad field %q", opts['k'])
		}
	}
	sep := opts['t']
	key := func(l string) string {
		if field == 0 {
			return l
		}
		var f []string
		if sep != "" {
			f = strings.Split(l, sep)
		} else {
			f = strings.Fields(l)
		}
		if field > len(f) {
			return ""
		}
		return strings.Join(f[field-1:], cmp.Or(sep, " "))
	}
	compare := func(a, b string) int {
		ka, kb := key(a), key(b)
		c := 0
		if opts['n'] != "" {
			c = cmp.Compare(leadingNumber(ka), leadingNumber(kb))
		}
		if c == 0 {
			c = strings.Compare(ka, kb)
		}
		if opts['r'] != "" {
			c = -c
		}
		return c
	}
	var all []string
	err = e.inputs(files, func(_ string, r io.Reader) error {
		return e.lines(r, func(l string) error {
			all = append(all, l)
			return nil
		})
	})
	if err != nil {
		return err
	}
	slices.SortStableFunc(all, compare)
	for i, l := range all {
		if opts['u'] != "" && i > 0 && compare(all[i-1], l) == 0 {
			continue
		}
		if err := e.println(l); err != nil {
			return err
		}
	}
	return nil
}

var numberPrefix = regexp.MustCompile(`^\s*-?[0-9]*\.?[0-9]*`)

func leadingNumber(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(numberPrefix.FindString(s)), 64)
	return f
}

// uniq supports -c and at most one input file.
func uniq(e *env, args []string) error {
	opts, files, err := getopt(args, "c", "")
	if err != nil {
		return err
	}
	if len(files) > 1 {
		return errors.New("only one input file is allowed")
	}
	prev, n := "", 0
	emit := func() error {
		if n == 0 {
			return nil
		}
		if opts['c'] != "" {
			return e.println(fmt.Sprintf("%7d %s", n, prev))
		}
		return e.println(prev)
	}
	err = e.inputs(files, func(_ string, r io.Reader) error {
		return e.lines(r, func(l string) error {
			if n > 0 && l == prev {
				n++
				return nil
			}
			err := emit()
			prev, n = l, 1
			return err
		})
	})
	if err != nil {
		return err
	}
	return emit()
}

// wc supports -l -w -c (and -m as -c). Without options it prints all three.
func wc(e *env, args []string) error {
	opts, files, err := getopt(args, "lwcm", "")
	if err != nil {
		return err
	}
	if opts['m'] != "" {
		opts['c'] = "1"
	}
	if len(opts) == 0 {
		opts = map[byte]string{'l': "1", 'w': "1", 'c': "1"}
	}
	return e.inputs(files, func(name string, r io.Reader) error {
		var lines, words, bytes int
		inWord := false
		br := bufio.NewReader(r)
		for {
			b, err := br.ReadByte()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			bytes++
			if b == '\n' {
				lines++
			}
			space := b == ' ' || b >= '\t' && b <= '\r'
			if !space && !inWord {
				words++
			}
			inWord = !space
		}
		var out []string
		for _, c := range []struct {
			opt byte
			n   int
		}{{'l', lines}, {'w', words}, {'c', bytes}} {
			if opts[c.opt] != "" {
				out = append(out, strconv.Itoa(c.n))
			}
		}
		if len(files) > 0 {
			out = append(out, name)
		}
		return e.println(strings.Join(out, " "))
	})
}

// cut supports -f LIST with -d DELIM (default tab), and -c LIST.
func cut(e *env, args []string) error {
	opts, files, err := getopt(args, "", "dfc")
	if err != nil {
		return err
	}
	list, byChar := opts['f'], false
	if list == "" {
		list, byChar = opts['c'], true
	}
	if list == "" {
		return errors.New("use -f LIST or -c LIST")
	}
	in, err := parseList(list)
	if err != nil {
		return err
	}
	delim := cmp.Or(opts['d'], "\t")
	return e.inputs(files, func(_ string, r io.Reader) error {
		return e.lines(r, func(l string) error {
			if byChar {
				var b strings.Builder
				for i, c := range []rune(l) {
					if in(i + 1) {
						b.WriteRune(c)
					}
				}
				return e.println(b.String())
			}
			if !strings.Contains(l, delim) {
				return e.println(l)
			}
			var keep []string
			for i, f := range strings.Split(l, delim) {
				if in(i + 1) {
					keep = append(keep, f)
				}
			}
			return e.println(strings.Join(keep, delim))
		})
	})
}

// parseList parses a cut list such as "1,3-5,7-" or "-2".
func parseList(list string) (func(int) bool, error) {
	type span struct{ lo, hi int }
	var spans []span
	for part := range strings.SplitSeq(list, ",") {
		lo, hi, isRange := strings.Cut(part, "-")
		s := span{1, 1 << 30}
		var err error
		if lo != "" {
			if s.lo, err = strconv.Atoi(lo); err != nil || s.lo < 1 {
				return nil, fmt.Errorf("bad list %q", list)
			}
		}
		if !isRange {
			s.hi = s.lo
		} else if hi != "" {
			if s.hi, err = strconv.Atoi(hi); err != nil || s.hi < s.lo {
				return nil, fmt.Errorf("bad list %q", list)
			}
		}
		spans = append(spans, s)
	}
	return func(i int) bool {
		return slices.ContainsFunc(spans, func(s span) bool { return i >= s.lo && i <= s.hi })
	}, nil
}

// tr supports -d and -s, ranges like a-z, \n \t \\ escapes and the classes
// [:alpha:] [:digit:] [:alnum:] [:upper:] [:lower:] [:space:]. It reads stdin.
func tr(e *env, args []string) error {
	opts, sets, err := getopt(args, "ds", "")
	if err != nil {
		return err
	}
	if len(sets) == 0 || len(sets) > 2 {
		return errors.New("needs one or two sets")
	}
	set1 := trSet(sets[0])
	var set2 []rune
	if len(sets) == 2 {
		set2 = trSet(sets[1])
	}
	del, squeeze := opts['d'] != "", opts['s'] != ""
	if !del && len(set2) == 0 && !squeeze {
		return errors.New("needs two sets")
	}
	squeezeSet := set2
	if len(squeezeSet) == 0 {
		squeezeSet = set1
	}
	br := bufio.NewReader(&capReader{r: e.stdin, left: maxInput})
	w := bufio.NewWriter(e.stdout)
	last := rune(-1)
	for {
		c, _, err := br.ReadRune()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if i := slices.Index(set1, c); i >= 0 {
			if del {
				continue
			}
			if len(set2) > 0 {
				c = set2[min(i, len(set2)-1)]
			}
		}
		if squeeze && c == last && slices.Contains(squeezeSet, c) {
			continue
		}
		last = c
		if _, err := w.WriteRune(c); err != nil {
			return err
		}
	}
	return w.Flush()
}

var trClasses = map[string]func(rune) bool{
	"[:alpha:]": unicode.IsLetter, "[:digit:]": unicode.IsDigit, "[:upper:]": unicode.IsUpper,
	"[:lower:]": unicode.IsLower, "[:space:]": unicode.IsSpace,
	"[:alnum:]": func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) },
}

// trSet expands a tr set to its runes. Classes cover ASCII only, so that
// [:upper:] and [:lower:] line up.
func trSet(s string) []rune {
	var out []rune
	for s != "" {
		if strings.HasPrefix(s, "[:") {
			if end := strings.Index(s, ":]"); end > 0 {
				if class, ok := trClasses[s[:end+2]]; ok {
					for r := rune(0); r < 128; r++ {
						if class(r) {
							out = append(out, r)
						}
					}
					s = s[end+2:]
					continue
				}
			}
		}
		c, rest := trRune(s)
		if len(rest) > 1 && rest[0] == '-' {
			if hi, after := trRune(rest[1:]); hi >= c {
				for r := c; r <= hi; r++ {
					out = append(out, r)
				}
				s = after
				continue
			}
		}
		out = append(out, c)
		s = rest
	}
	return out
}

// trRune reads one rune of a tr set, with the escapes \n \t \r and \X for X.
func trRune(s string) (rune, string) {
	if len(s) > 1 && s[0] == '\\' {
		switch s[1] {
		case 'n':
			return '\n', s[2:]
		case 't':
			return '\t', s[2:]
		case 'r':
			return '\r', s[2:]
		}
		r, n := utf8.DecodeRuneInString(s[1:])
		return r, s[1+n:]
	}
	r, n := utf8.DecodeRuneInString(s)
	return r, s[n:]
}

// jq is the internal/shell jq with confined files, capped input and an
// empty environment.
func jq(e *env, args []string) error {
	return shell.JQ(e.ctx, append([]string{"jq"}, args...), &capReader{r: e.stdin, left: maxInput},
		e.stdout, e.stderr, e.open, func() []string { return nil })
}
