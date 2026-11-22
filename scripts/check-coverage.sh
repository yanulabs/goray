#!/bin/sh
set -eu

PROFILE="${COVER_PROFILE:-coverage.out}"
RESULTS="${COVER_RESULTS:-coverage.txt}"
TOTAL_MIN="${COVER_TOTAL_MIN:-80}"
TOOLS_MIN="${COVER_TOOLS_MIN:-75}"
REPORT_MIN="${COVER_REPORT_MIN:-75}"

go test -count=1 -coverprofile="$PROFILE" ./... | tee "$RESULTS"

check_min() {
	label="$1"
	got="$2"
	min="$3"
	if ! awk -v got="$got" -v min="$min" 'BEGIN { exit !(got + 0 >= min + 0) }'; then
		echo "coverage: $label is $got%, minimum is $min%" >&2
		exit 1
	fi
}

check_package() {
	pkg="$1"
	min="$2"
	got="$(awk -v pkg="$pkg" '$2 == pkg { gsub("%", "", $5); print $5 }' "$RESULTS" | tail -1)"
	if [ -z "$got" ]; then
		echo "coverage: missing result for $pkg" >&2
		exit 1
	fi
	check_min "$pkg" "$got" "$min"
}

check_package "github.com/yanulabs/goray/internal/tools" "$TOOLS_MIN"
check_package "github.com/yanulabs/goray/internal/report" "$REPORT_MIN"

total="$(go tool cover -func="$PROFILE" | awk '/^total:/ { gsub("%", "", $3); print $3 }')"
if [ -z "$total" ]; then
	echo "coverage: missing total" >&2
	exit 1
fi
check_min "total" "$total" "$TOTAL_MIN"
echo "coverage: total $total%, tools >= $TOOLS_MIN%, report >= $REPORT_MIN%"
