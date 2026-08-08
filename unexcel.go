package main

import (
	"bufio"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/xuri/excelize/v2"
	"golang.org/x/text/language"
)

const endOfSheetName = byte(0x0b)
const endOfSheet = byte(0x0c)

//go:embed locales/*.json
var localeFS embed.FS
var loc *i18n.Localizer

var version = "(devel)"

func T(id string) string {
	return loc.MustLocalize(&i18n.LocalizeConfig{MessageID: id})
}

func Tf(id string, data map[string]interface{}) string {
	return loc.MustLocalize(&i18n.LocalizeConfig{MessageID: id, TemplateData: data})
}

func newLocalizer(lang string) *i18n.Localizer {
	bundle := i18n.NewBundle(language.English)
	bundle.RegisterUnmarshalFunc("json", json.Unmarshal)
	if _, err := bundle.LoadMessageFileFS(localeFS, "locales/en.json"); err != nil {
		panic(err)
	}
	if _, err := bundle.LoadMessageFileFS(localeFS, "locales/ja.json"); err != nil {
		panic(err)
	}
	return i18n.NewLocalizer(bundle, lang, "en")
}

func detectLang() string {
	for _, env := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(env); v != "" {
			return normalizeLang(v)
		}
	}
	return "en"
}

// normalizeLang は "ja_JP.UTF-8" のようなロケール文字列をBCP47タグへ簡易変換する。
// 本ツールが対応するのは英語・日本語のみのため、先頭が"ja"かどうかだけで判定する。
func normalizeLang(s string) string {
	base := strings.SplitN(s, ".", 2)[0]
	base = strings.SplitN(base, "_", 2)[0]
	base = strings.ToLower(base)
	if strings.HasPrefix(base, "ja") {
		return "ja"
	}
	return "en"
}

// jsonFlag: "-json" と "-json=FILE" の両方を受け付ける。
type jsonFlag struct {
	set  bool
	file string
}

func (j *jsonFlag) String() string {
	if j == nil {
		return ""
	}
	return j.file
}

func (j *jsonFlag) Set(s string) error {
	j.set = true
	if s == "true" {
		j.file = ""
		return nil
	}
	j.file = s
	return nil
}

func (j *jsonFlag) IsBoolFlag() bool { return true }

type outDirFlag struct {
	set bool
	dir string
}

func (o *outDirFlag) String() string {
	if o == nil {
		return ""
	}
	return o.dir
}

func (o *outDirFlag) Set(s string) error {
	o.set = true
	if s == "true" {
		o.dir = ""
		return nil
	}
	o.dir = s
	return nil
}

func (o *outDirFlag) IsBoolFlag() bool { return true }

func main() {
	loc = newLocalizer(detectLang())

	var outDirOut outDirFlag
	flag.Var(&outDirOut, "outdir", T("flagOutdirUsage"))
	raw := flag.Bool("raw", false, T("flagRawUsage"))
	noHeader := flag.Bool("noheader", false, T("flagNoHeaderUsage"))
	var jsonOut jsonFlag
	flag.Var(&jsonOut, "json", T("flagJSONUsage"))
	pretty := flag.Bool("pretty", false, T("flagPrettyUsage"))
	flag.Parse()

	if flag.NArg() == 0 {
		fmt.Fprintf(os.Stderr, "%s %s\n", filepath.Base(os.Args[0]), version)
		fmt.Fprintln(os.Stderr, T("usageTsv"))
		fmt.Fprintln(os.Stderr, T("usageJson"))
		os.Exit(0)
	}
	inputPath := flag.Arg(0)

	if !jsonOut.set && outDirOut.set && outDirOut.dir == "" {
		fmt.Fprintf(os.Stderr, "%s: %s\n", T("errorLabel"), T("errOutdirNeedsDir"))
		os.Exit(1)
	}

	if err := run(inputPath, outDirOut.dir, *raw, *noHeader, jsonOut.set, jsonOut.file, *pretty); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", T("errorLabel"), err)
		os.Exit(1)
	}
}

func run(inputPath, outDir string, raw, noHeader, jsonMode bool, jsonFile string, pretty bool) error {
	f, err := openWorkbook(inputPath)
	if err != nil {
		return fmt.Errorf("%s", Tf("errOpenWorkbook", map[string]interface{}{"Err": err}))
	}
	defer func() {
		_ = f.Close()
	}()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return fmt.Errorf("%s", T("errNoSheets"))
	}

	if jsonMode {
		return runJSON(f, sheets, jsonFile, pretty)
	}

	if outDir == "" {
		return runStream(f, sheets, os.Stdout, raw, noHeader)
	}
	return runFiles(f, sheets, outDir, raw, noHeader)
}

func openWorkbook(inputPath string) (*excelize.File, error) {
	if inputPath == "-" {
		return excelize.OpenReader(os.Stdin)
	}
	return excelize.OpenFile(inputPath)
}

func runStream(f *excelize.File, sheets []string, out io.Writer, raw, noHeader bool) error {
	w := bufio.NewWriter(out)
	for _, sheet := range sheets {
		if _, err := w.WriteString(sheet); err != nil {
			return err
		}
		if _, err := w.Write([]byte{endOfSheetName}); err != nil {
			return err
		}
		if err := writeSheetTSV(f, sheet, w, raw, noHeader); err != nil {
			return fmt.Errorf("%s", Tf("errConvertSheet", map[string]interface{}{"Sheet": sheet, "Err": err}))
		}
		if err := w.Flush(); err != nil {
			return err
		}
		if _, err := w.Write([]byte{endOfSheet}); err != nil {
			return err
		}
		if err := w.Flush(); err != nil {
			return err
		}
	}
	return nil
}

func runFiles(f *excelize.File, sheets []string, outDir string, raw, noHeader bool) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("%s", Tf("errMkdirOutdir", map[string]interface{}{"Err": err}))
	}

	usedNames := make(map[string]int)
	for _, sheet := range sheets {
		outPath := outputPathForSheet(outDir, sheet, usedNames)
		if err := convertSheetToFile(f, sheet, outPath, raw, noHeader); err != nil {
			return fmt.Errorf("%s", Tf("errConvertSheet", map[string]interface{}{"Sheet": sheet, "Err": err}))
		}
		fmt.Println(Tf("sheetConverted", map[string]interface{}{"Sheet": sheet, "Path": outPath}))
	}
	return nil
}

// builtinNumFmt: OOXML組み込み数値書式コード(ID 0-49)。
var builtinNumFmt = map[int]string{
	0:  "general",
	1:  "0",
	2:  "0.00",
	3:  "#,##0",
	4:  "#,##0.00",
	9:  "0%",
	10: "0.00%",
	11: "0.00E+00",
	12: "# ?/?",
	13: "# ??/??",
	14: "mm-dd-yy",
	15: "d-mmm-yy",
	16: "d-mmm",
	17: "mmm-yy",
	18: "h:mm AM/PM",
	19: "h:mm:ss AM/PM",
	20: "hh:mm",
	21: "hh:mm:ss",
	22: "m/d/yy hh:mm",
	37: "#,##0 ;(#,##0)",
	38: "#,##0 ;[red](#,##0)",
	39: "#,##0.00 ;(#,##0.00)",
	40: "#,##0.00 ;[red](#,##0.00)",
	41: `_(* #,##0_);_(* \(#,##0\);_(* "-"_);_(@_)`,
	42: `_("$"* #,##0_);_("$"* \(#,##0\);_("$"* "-"_);_(@_)`,
	43: `_(* #,##0.00_);_(* \(#,##0.00\);_(* "-"??_);_(@_)`,
	44: `_("$"* #,##0.00_);_("$"* \(#,##0.00\);_("$"* "-"??_);_(@_)`,
	45: "mm:ss",
	46: "[h]:mm:ss",
	47: "mm:ss.0",
	48: "##0.0E+0",
	49: "@",
}

func cellNumFmt(f *excelize.File, sheet, cellRef string) (string, error) {
	styleID, err := f.GetCellStyle(sheet, cellRef)
	if err != nil {
		return "", err
	}
	style, err := f.GetStyle(styleID)
	if err != nil {
		return "", err
	}
	if style.CustomNumFmt != nil && *style.CustomNumFmt != "" {
		return *style.CustomNumFmt, nil
	}
	if code, ok := builtinNumFmt[style.NumFmt]; ok {
		return code, nil
	}
	return "", nil
}

const jsonIndentWidth = 4

func writeJSONIndent(w *bufio.Writer, pretty bool, level int) error {
	if !pretty {
		return nil
	}
	_, err := w.WriteString("\n" + strings.Repeat(" ", level*jsonIndentWidth))
	return err
}

func writeJSONColonSep(w *bufio.Writer, pretty bool) error {
	sep := ":"
	if pretty {
		sep = ": "
	}
	_, err := w.WriteString(sep)
	return err
}

func runJSON(f *excelize.File, sheets []string, jsonFile string, pretty bool) error {
	var out io.Writer = os.Stdout
	if jsonFile != "" {
		file, err := os.Create(jsonFile)
		if err != nil {
			return fmt.Errorf("%s", Tf("errCreateJSONFile", map[string]interface{}{"Err": err}))
		}
		defer func() {
			_ = file.Close()
		}()
		out = file
	}

	w := bufio.NewWriter(out)
	if _, err := w.WriteString("{"); err != nil {
		return err
	}
	for i, sheet := range sheets {
		if i > 0 {
			if _, err := w.WriteString(","); err != nil {
				return err
			}
		}
		if err := writeJSONIndent(w, pretty, 1); err != nil {
			return err
		}
		if err := writeJSONString(w, sheet); err != nil {
			return err
		}
		if err := writeJSONColonSep(w, pretty); err != nil {
			return err
		}
		if err := writeSheetJSON(f, sheet, w, pretty); err != nil {
			return fmt.Errorf("%s", Tf("errConvertSheet", map[string]interface{}{"Sheet": sheet, "Err": err}))
		}
	}
	if err := writeJSONIndent(w, pretty, 0); err != nil {
		return err
	}
	if _, err := w.WriteString("}\n"); err != nil {
		return err
	}
	return w.Flush()
}

func writeSheetJSON(f *excelize.File, sheet string, w *bufio.Writer, pretty bool) error {
	rows, err := f.Rows(sheet)
	if err != nil {
		return err
	}
	defer func() {
		_ = rows.Close()
	}()

	if _, err := w.WriteString("{"); err != nil {
		return err
	}

	rowNum := 0
	first := true
	for rows.Next() {
		rowNum++
		cols, err := rows.Columns()
		if err != nil {
			return err
		}
		for c := 1; c <= len(cols); c++ {
			colName, err := excelize.ColumnNumberToName(c)
			if err != nil {
				return err
			}
			cellRef := colName + strconv.Itoa(rowNum)
			cached := cols[c-1]

			formula, ferr := f.GetCellFormula(sheet, cellRef)
			hasFormula := ferr == nil && formula != ""

			value := cached
			if hasFormula && cached == "" {
				if calcVal, cerr := f.CalcCellValue(sheet, cellRef); cerr == nil {
					value = calcVal
				}
			}

			if value == "" && !hasFormula {
				continue
			}

			format, ferr2 := cellNumFmt(f, sheet, cellRef)
			hasFormat := ferr2 == nil && format != "" && format != "general" && format != "@"

			if !first {
				if _, err := w.WriteString(","); err != nil {
					return err
				}
			}
			first = false

			if err := writeJSONIndent(w, pretty, 2); err != nil {
				return err
			}
			if err := writeJSONCell(w, cellRef, value, format, hasFormat, formula, hasFormula, pretty); err != nil {
				return err
			}
		}
	}
	if err := rows.Error(); err != nil {
		return err
	}

	if !first {
		if err := writeJSONIndent(w, pretty, 1); err != nil {
			return err
		}
	}
	_, err = w.WriteString("}")
	return err
}

func writeJSONCell(w *bufio.Writer, cellRef, value, format string, hasFormat bool, formula string, hasFormula bool, pretty bool) error {
	if err := writeJSONString(w, cellRef); err != nil {
		return err
	}
	if err := writeJSONColonSep(w, pretty); err != nil {
		return err
	}
	if _, err := w.WriteString("{"); err != nil {
		return err
	}

	if err := writeJSONIndent(w, pretty, 3); err != nil {
		return err
	}
	if _, err := w.WriteString(`"value"`); err != nil {
		return err
	}
	if err := writeJSONColonSep(w, pretty); err != nil {
		return err
	}
	if err := writeJSONString(w, value); err != nil {
		return err
	}

	if hasFormat {
		if _, err := w.WriteString(","); err != nil {
			return err
		}
		if err := writeJSONIndent(w, pretty, 3); err != nil {
			return err
		}
		if _, err := w.WriteString(`"format"`); err != nil {
			return err
		}
		if err := writeJSONColonSep(w, pretty); err != nil {
			return err
		}
		if err := writeJSONString(w, format); err != nil {
			return err
		}
	}

	if hasFormula {
		if _, err := w.WriteString(","); err != nil {
			return err
		}
		if err := writeJSONIndent(w, pretty, 3); err != nil {
			return err
		}
		if _, err := w.WriteString(`"raw"`); err != nil {
			return err
		}
		if err := writeJSONColonSep(w, pretty); err != nil {
			return err
		}
		if err := writeJSONString(w, "="+strings.TrimPrefix(formula, "=")); err != nil {
			return err
		}
	}

	if err := writeJSONIndent(w, pretty, 2); err != nil {
		return err
	}
	_, err := w.WriteString("}")
	return err
}

func writeJSONString(w *bufio.Writer, s string) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

func convertSheetToFile(f *excelize.File, sheet, outPath string, raw, noHeader bool) error {
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
	}()

	w := bufio.NewWriter(out)
	if err := writeSheetTSV(f, sheet, w, raw, noHeader); err != nil {
		return err
	}
	return w.Flush()
}

func writeSheetTSV(f *excelize.File, sheet string, w io.Writer, raw, noHeader bool) error {
	maxCol, err := sheetMaxCol(f, sheet)
	if err != nil {
		return err
	}
	if maxCol == 0 {
		maxCol = 1
	}

	if !noHeader {
		if err := writeHeaderRow(w, maxCol); err != nil {
			return err
		}
	}

	rows, err := f.Rows(sheet)
	if err != nil {
		return err
	}
	defer func() {
		_ = rows.Close()
	}()

	rowNum := 0
	for rows.Next() {
		rowNum++
		cols, err := rows.Columns()
		if err != nil {
			return err
		}
		if err := writeDataRow(w, f, sheet, rowNum, cols, maxCol, raw, noHeader); err != nil {
			return err
		}
	}
	return rows.Error()
}

// sheetMaxCol: dimension属性は信頼せず実データを走査して最大列数を求める。
func sheetMaxCol(f *excelize.File, sheet string) (int, error) {
	rows, err := f.Rows(sheet)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = rows.Close()
	}()

	maxCol := 0
	for rows.Next() {
		cols, err := rows.Columns()
		if err != nil {
			return 0, err
		}
		if len(cols) > maxCol {
			maxCol = len(cols)
		}
	}
	return maxCol, nil
}

func writeHeaderRow(w io.Writer, maxCol int) error {
	for c := 1; c <= maxCol; c++ {
		colName, err := excelize.ColumnNumberToName(c)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(w, "\t"); err != nil {
			return err
		}
		if _, err := io.WriteString(w, colName); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "\n")
	return err
}

func writeDataRow(w io.Writer, f *excelize.File, sheet string, rowNum int, cols []string, maxCol int, raw, noHeader bool) error {
	if !noHeader {
		if _, err := io.WriteString(w, strconv.Itoa(rowNum)); err != nil {
			return err
		}
	}
	for c := 1; c <= maxCol; c++ {
		colName, err := excelize.ColumnNumberToName(c)
		if err != nil {
			return err
		}

		if !noHeader || c > 1 {
			if _, err := io.WriteString(w, "\t"); err != nil {
				return err
			}
		}

		var cached string
		if c-1 < len(cols) {
			cached = cols[c-1]
		}

		cellRef := colName + strconv.Itoa(rowNum)
		formula, ferr := f.GetCellFormula(sheet, cellRef)
		hasFormula := ferr == nil && formula != ""

		out := cached
		switch {
		case hasFormula && raw:
			out = "=" + strings.TrimPrefix(formula, "=")
		case hasFormula && cached != "":
			// キャッシュ値を使用(再計算しない)
		case hasFormula:
			if calcVal, cerr := f.CalcCellValue(sheet, cellRef); cerr == nil {
				out = calcVal
			}
		}

		if _, err := io.WriteString(w, escapeTSV(out)); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "\n")
	return err
}

func escapeTSV(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\t", "\\t")
	s = strings.ReplaceAll(s, "\r\n", "\\n")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\n")
	return s
}

var invalidFileChars = regexp.MustCompile(`[\\/:*?"<>|]`)

func outputPathForSheet(outDir, sheet string, usedNames map[string]int) string {
	name := invalidFileChars.ReplaceAllString(sheet, "_")
	name = strings.TrimSpace(name)
	if name == "" {
		name = "sheet"
	}

	usedNames[name]++
	if n := usedNames[name]; n > 1 {
		name = fmt.Sprintf("%s_%d", name, n)
	}

	return filepath.Join(outDir, name+".tsv")
}
