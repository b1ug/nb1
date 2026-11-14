# ColorInput v1

This package owns nb1's bounded, flat JSON color grammar described in
the [API v1 color contract](../../docs/api-v1.md#colors). It accepts a bare ColorInput or an explicit
`{"color": ...}` envelope, rejects duplicate and unknown fields, and does not
read configuration, devices, environment variables, or files.

CSS names and Hex literals are parsed by `github.com/b1ug/colorkit`. HSV objects
pass the original `json.Number.String()` tokens directly to
`colorkit.FromHSVDecimal`; they never pass through floating point. The shared
core validates decimal syntax and ranges, computes exact rational HSV, and
rounds half up once to opaque 8-bit RGB. This package applies no Gamma,
brightness adjustment, or optical calibration.

The result adapter preserves the existing schema version, contract,
`input_kind`, canonical lowercase Hex, and `css-named-2026` palette marker.
The local `RGB8` DTO intentionally remains an integer `{r,g,b}` JSON object;
aliasing it to `colorkit.RGB8` would change JSON to a Hex string. HTTP, CLI,
MCP, and Sequence callers share this boundary rather than duplicating color
conversion. String inputs remain limited to CSS names and `#rgb`/`#rrggbb`;
function syntax is not part of this JSON grammar.

`ValidateResult` checks known schema/contract/input kinds, canonical Hex/RGB
agreement, and palette rules. It does not prove which original input a remote
resolver received. Wire callers must still reject unknown, duplicate, null,
or missing fields. The full input remains limited to 4096 bytes and color
strings to 64 decoded bytes before trimming.

`testdata/css-named-2026.tsv` is the unchanged independent fixture for all 148
opaque CSS names and aliases, excluding transparent, currentColor, and system
colors. Its source is the [W3C CSS Color 4 draft from November 3, 2026](https://www.w3.org/TR/2026/CRD-css-color-4-20261103/#named-colors):
HTML SHA-256 `b66ad2af540a7550fd34e0d95f8dce3f49b9d39e255fdfed56f0ec1de9c78000`,
sorted TSV SHA-256 `521686f82ac6c649de3f22589783ac86e2bfeb0c1858fd953f59efc4e1d2fd09`.
Tests compare every name, value, spelling, and alias through this package's
public resolver. The 37 unchanged HSV goldens were independently generated
with Python Fraction using C/X/m, covering all six sectors, 0/360 degrees,
exact half values, and adjacent six-place decimals. Tests also cover strict
JSON/numeric syntax, input limits, result validation, and concurrent use.
