# unexcel

A tool for exporting Excel workbooks (*.xlsx) as TSV or JSON.

## TSV Output Mode (default)

Outputs data in TSV format to standard output or a specified directory.

When outputting to standard output, multiple sheets are separated using the control codes VT (Vertical Tab; 0x0b) and FF (Form Feed; 0x0c).

```
Sheet 1 name<VT>
Sheet 1 data (TSV)<FF>
Sheet 2 name<VT>
Sheet 2 data (TSV)<FF>
...
```

If an output directory is specified with `-outdir=<output directory>`, a `<output directory>/<sheet name>.tsv` file is created for each sheet.
Characters that cannot be used in sheet names (`\ / : * ? " < > |`) are replaced with `_`, and a sequential number is appended in case of duplicates.

### Options

- Headers are output following Excel's display convention (A, B, C... / 1, 2, 3...). They can be hidden with the `-noheader` option.
- If you want to output formulas (e.g. `=SUM(A1:A10)`) instead of calculated results, specify the `-raw` option.
- Tabs and newlines within cells are converted to `\t` and `\n`.

## JSON Output Mode

Outputs the entire workbook in JSON format using `-json[=output file name]`.
If no file name is specified, output goes to standard output; if a file name is specified, output goes to that file.
Use the `-pretty` option to output formatted (pretty-printed) JSON.

```json
{
	"Sheet name": {
		"A1": { "value": "200", "format": "0.0", "raw": "=SUM(B1,B2)" }
	}
}
```

## Specifying Input

- Specify the path to an XLSX file
- Specify `-` to read XLSX data from standard input

```sh
# Read from a specified file
unexcel sample.xlsx

# Read from standard input
curl https://example.com/download/example.xlsx | ./unexcel -
```

## Build

```sh
make
```

(Dependency library: github.com/xuri/excelize/v2)
