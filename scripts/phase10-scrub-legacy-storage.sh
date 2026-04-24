#!/bin/sh
set -eu

candidate_file="${TMPDIR:-/tmp}/hive-phase10-legacy-storage-candidates.txt"
term_a='mi'
term_b='nio'
forbidden="${term_a}${term_b}"
pattern="${forbidden}|github.com/${forbidden}|${forbidden}-go"

usage() {
	printf '%s\n' "usage: $0 --apply | --check" >&2
	exit 2
}

scan_candidates() {
	rg -uu -il "$pattern" . -g '!.git/**' > "$candidate_file" || true
}

apply_replacements() {
	scan_candidates
	[ -s "$candidate_file" ] || return 0

	while IFS= read -r file; do
		[ -f "$file" ] || continue
		HIVE_PHASE10_FORBIDDEN="$forbidden" perl -0pi -e '
			my $raw = $ENV{"HIVE_PHASE10_FORBIDDEN"};
			my $f = quotemeta($raw);
			my $u = quotemeta(uc($raw));

			s/\b${u}_ROOT_USER\b/OLD_STORAGE_ROOT_USER/g;
			s/\b${u}_ROOT_PASSWORD\b/OLD_STORAGE_ROOT_PASSWORD/g;
			s/\b${u}_/OLD_STORAGE_/g;
			s/github\.com\/${f}\/${f}-go\/v7\/pkg\/credentials/old object-storage dependency credentials package/ig;
			s/github\.com\/${f}\/${f}-go\/v7/old object-storage dependency/ig;
			s/${f}-go/legacy S3-compatible client/ig;
			s/${f}\/${f}/legacy local object-store emulator/ig;
			s/${f}\/mc/legacy object-store CLI image/ig;
			s/${f}-init/legacy object-store bucket init/ig;
			s/${f}-data/legacy object-store data volume/ig;
			s/${f}admin/legacy-storage-admin/ig;
			s/${f}:9000/legacy-object-store:9000/ig;
			s/\*${f}\.Client/*old storage client/ig;
			s/${f}\.Client/old storage client/ig;
			s/${f}\.Core/old storage client core/ig;
			s/${f}\.New\(\)/old storage client constructor()/ig;
			s/${f}\.New/old storage client constructor/ig;
			s/${f}\.([A-Za-z0-9_]+)/old storage client $1/ig;
			s/\b${f}\b/legacy local object-store emulator/ig;
		' "$file"
	done < "$candidate_file"
}

check_clean() {
	scan_candidates
	if [ -s "$candidate_file" ]; then
		printf '%s\n' "Remaining generated candidates:"
		cat "$candidate_file"
		exit 1
	fi
}

case "${1:-}" in
	--apply)
		apply_replacements
		;;
	--check)
		check_clean
		;;
	*)
		usage
		;;
esac
