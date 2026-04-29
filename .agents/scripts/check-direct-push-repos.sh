#!/usr/bin/env bash
# Verify that helper direct-push repo policy matches direct-push-repos.txt.

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

source_file=""
for candidate in "$script_dir/direct-push-repos.txt" "$script_dir/../direct-push-repos.txt"; do
  if [[ -f "$candidate" ]]; then
    source_file="$candidate"
    break
  fi
done

helper=""
for candidate in "$script_dir/dev-cycle-helper.sh" "$script_dir/../scripts/dev-cycle-helper.sh"; do
  if [[ -x "$candidate" ]]; then
    helper="$candidate"
    break
  fi
done

if [[ -z "$source_file" || -z "$helper" ]]; then
  echo "Missing direct-push helper files" >&2
  exit 1
fi

expected_list() {
  sed 's/^[[:space:]]*//; s/[[:space:]]*$//; /^[[:space:]]*$/d; /^#/d' "$source_file" | sort
}

expected="$(expected_list | paste -sd '|' -)"
actual="$("$helper" direct-push-list | sort | paste -sd '|' -)"

if [[ -z "$expected" ]]; then
  echo "direct-push-repos.txt is empty" >&2
  exit 1
fi

if [[ "$actual" != "$expected" ]]; then
  echo "direct-push list drift in scripts/dev-cycle-helper.sh" >&2
  echo "expected: $expected" >&2
  echo "actual:   $actual" >&2
  exit 1
fi
