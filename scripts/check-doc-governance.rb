#!/usr/bin/env ruby
# frozen_string_literal: true

require "pathname"
require "set"

ROOT = Pathname.new(__dir__).parent.expand_path
STRICT = ENV["DOC_GOVERNANCE_STRICT"] == "1" || ARGV.include?("--strict")

ID_PATTERN = /(?<![A-Za-z0-9_])(?:Q|DEC|ADR|REQ|NFR|AC|TEST|SPIKE|TASK|TRACE|ASM)-\d{3,4}(?![A-Za-z0-9_#-])/
AC_ID_PATTERN = /(?<![A-Za-z0-9_])AC-\d{3,4}(?![A-Za-z0-9_#-])/
REFERENCE_LINK_DEFINITION_PATTERN = /^\s{0,3}\[([^\]]+)\]:\s*(<[^>]+>|\S+)/
PLACEHOLDER_PATTERNS = [
  /\b(?:Q|DEC|ADR|REQ|NFR|AC|TEST|SPIKE|TASK|TRACE|ASM)-(?:###|####)(?![A-Za-z0-9_#-])/,
  /^\s{0,3}#+\s+(?:Q|DEC|ADR|REQ|NFR|AC|TEST|SPIKE|TASK|TRACE|ASM)-\d{3,4}:\s*(?:\.{3}|…)\s*$/,
  /<(?:질문 한 줄|결정 한 줄|한 줄 제목|이름|Milestone name|YYYY-MM-DD)>/,
  /(?:Opened:|proposed —)\s*<date>/,
  /^\s*Status:\s*template\.\s*$/i
].freeze

EXCLUDED_ACTIVE_PREFIXES = [
  "docs/templates/",
  "docs/discovery/",
  "docs/design/archive/",
  "docs/generated/"
].freeze

Definition = Struct.new(:id, :path, :line, keyword_init: true)
Reference = Struct.new(:id, :path, :line, keyword_init: true)

def relative(path)
  Pathname.new(path).expand_path.relative_path_from(ROOT).to_s
end

def active_doc?(path)
  rel = relative(path)
  return false unless rel.end_with?(".md")
  return false if rel.start_with?(".git/")

  EXCLUDED_ACTIVE_PREFIXES.none? { |prefix| rel.start_with?(prefix) }
end

def markdown_files
  tracked = IO.popen(["git", "-C", ROOT.to_s, "ls-files", "-z"], &:read)
  tracked_files = tracked.split("\0").select { |path| path.end_with?(".md") }
  return tracked_files.sort.map { |path| ROOT.join(path) } unless tracked_files.empty?

  Dir.glob(ROOT.join("**/*.md"), File::FNM_DOTMATCH).sort.map { |path| Pathname.new(path) }
end

def reject_symlinked_markdown(files)
  files.each_with_object([]) do |path, errors|
    next unless path.symlink?

    errors << "#{relative(path)} is a symlinked Markdown file; replace it with a regular file before running governance checks"
  end
end

def content_lines(path)
  fence = nil
  html_comment = false
  path.readlines(chomp: true).each_with_index do |line, index|
    if fence
      if (info = fence_marker(line, allow_blockquote: fence[:quote_depth].positive?))
        marker = info[:marker]
        marker_char = marker[0]
        if info[:quote_depth] == fence[:quote_depth] &&
            marker_char == fence[:char] &&
            marker.length >= fence[:length] &&
            info[:trailing].strip.empty?
          fence = nil
        end
      end
      next
    end

    unless html_comment
      if (info = fence_marker(line))
        marker = info[:marker]
        fence = { char: marker[0], length: marker.length, quote_depth: info[:quote_depth] }
        next
      end
      next if indented_code_line?(line)
    end

    line, html_comment = without_html_comments(line, html_comment)
    if (info = fence_marker(line))
      marker = info[:marker]
      fence = { char: marker[0], length: marker.length, quote_depth: info[:quote_depth] }
      next
    end
    next if indented_code_line?(line)

    yield line, index + 1
  end
end

def fence_marker(line, allow_blockquote: true)
  quote_depth, content = allow_blockquote ? blockquote_depth_and_content(line) : [0, line]
  match = content.match(/\A {0,3}(`{3,}|~{3,})(.*)\z/)
  return nil unless match

  { marker: match[1], quote_depth: quote_depth, trailing: match[2] }
end

def blockquote_depth_and_content(line)
  content = line
  depth = 0
  loop do
    stripped = content.sub(/\A {0,3}>\s?/, "")
    return [depth, content] if stripped == content

    content = stripped
    depth += 1
  end
end

def without_html_comments(line, in_comment)
  rendered = +""
  index = 0

  while index < line.length
    if in_comment
      closing = line.index("-->", index)
      return [rendered, true] unless closing

      in_comment = false
      index = closing + 3
      next
    end

    opening = line.index("<!--", index)
    unless opening
      rendered << line[index..]
      break
    end

    rendered << line[index...opening]
    closing = line.index("-->", opening + 4)
    return [rendered, true] unless closing

    index = closing + 3
  end

  [rendered, in_comment]
end

def indented_code_line?(line)
  return false unless line.match?(/^(?: {4}|\t)/)

  !line.match?(/^(?: {4}|\t)\s*(?:[-*+]\s+|\d+[.)]\s+|>\s*|\|)/)
end

def inline_code_text(line, preserve_single: false)
  rendered = +""
  index = 0

  while index < line.length
    unless line[index] == "`"
      rendered << line[index]
      index += 1
      next
    end

    tick_end = index
    tick_end += 1 while tick_end < line.length && line[tick_end] == "`"
    delimiter = "`" * (tick_end - index)
    closing = line.index(delimiter, tick_end)

    unless closing
      rendered << delimiter
      index = tick_end
      next
    end

    rendered << line[tick_end...closing] if preserve_single && delimiter.length == 1
    index = closing + delimiter.length
  end

  rendered
end

def reference_code_text(line)
  # Single-backtick IDs are common live references; multi-backtick spans are
  # treated as literal examples.
  inline_code_text(line, preserve_single: true)
end

def rendered_inline_links(line)
  rendered = +""
  index = 0

  while (marker = line.index("](", index))
    label_start = line.rindex("[", marker)
    unless label_start && label_start >= index
      rendered << line[index, marker + 2 - index]
      index = marker + 2
      next
    end

    image = label_start.positive? && line[label_start - 1] == "!"
    link_start = image ? label_start - 1 : label_start
    depth = 0
    position = marker + 2

    while position < line.length
      char = line[position]
      if char == "\\" && position + 1 < line.length
        position += 2
        next
      end

      if char == "("
        depth += 1
      elsif char == ")"
        break if depth.zero?

        depth -= 1
      end

      position += 1
    end

    unless position < line.length && line[position] == ")"
      rendered << line[index, marker + 2 - index]
      index = marker + 2
      next
    end

    rendered << line[index...link_start]
    rendered << line[(label_start + 1)...marker] unless image
    index = position + 1
  end

  rendered << line[index..] if index < line.length
  rendered
end

def reference_scan_line(line)
  return "" if line.match?(REFERENCE_LINK_DEFINITION_PATTERN)

  scan_line = reference_code_text(line)
    .gsub(%r{<https?://[^>\s]+>}, "")
    .gsub(%r{\bhttps?://\S+}, "")
  rendered_inline_links(scan_line)
    .gsub(/(?<!!)\[([^\]]+)\]\[[^\]]*\]/, "\\1")
    .gsub(/!\[[^\]]*\]\[[^\]]*\]/, "")
end

def record_definition(definitions, id, path, line)
  definitions[id] << Definition.new(id: id, path: relative(path), line: line)
end

def collect_definitions_and_references(files)
  definitions = Hash.new { |hash, key| hash[key] = [] }
  references = []
  adr_filename_errors = []

  files.each do |path|
    file_defined_ids = Set.new

    content_lines(path) do |line, number|
      reference_scan_line(line).scan(ID_PATTERN) do |id|
        references << Reference.new(id: id, path: relative(path), line: number)
      end

      if (match = line.match(/^\s{0,3}#+\s+(#{ID_PATTERN.source})(?::|\b)/))
        record_definition(definitions, match[1], path, number)
        file_defined_ids << match[1]
      end

      if (match = line.match(/^\s*\|\s*(#{ID_PATTERN.source})\s*\|/))
        record_definition(definitions, match[1], path, number)
        file_defined_ids << match[1]
      end

      next unless (match = line.match(/^\s*[-*]\s+(ASM-\d{3,4})\s*:/))

      record_definition(definitions, match[1], path, number)
      file_defined_ids << match[1]
    end

    rel = relative(path)
    next unless rel.start_with?("docs/adr/")
    next unless (match = File.basename(rel).match(/\A(?:ADR-)?(\d{3,4})(?:[-_.]|$)/i))

    id = "ADR-#{match[1]}"
    defined_adr_ids = file_defined_ids.select { |defined_id| defined_id.start_with?("ADR-") }
    if defined_adr_ids.empty?
      record_definition(definitions, id, path, 1)
    elsif !defined_adr_ids.include?(id)
      adr_filename_errors << "#{rel}:1 ADR filename implies #{id} but content defines #{defined_adr_ids.to_a.sort.join(', ')}"
    end
  end

  [definitions, references, adr_filename_errors]
end

def check_duplicate_definitions(definitions)
  definitions.each_with_object([]) do |(id, locations), errors|
    next unless locations.length > 1

    where = locations.map { |location| "#{location.path}:#{location.line}" }.join(", ")
    errors << "duplicate definition for #{id}: #{where}"
  end
end

def check_dangling_references(definitions, references)
  defined_ids = definitions.keys.to_set
  missing = references.reject { |reference| defined_ids.include?(reference.id) }

  missing.group_by(&:id).map do |id, refs|
    where = refs.first(5).map { |ref| "#{ref.path}:#{ref.line}" }.join(", ")
    suffix = refs.length > 5 ? " (+#{refs.length - 5} more)" : ""
    "dangling reference to #{id}: #{where}#{suffix}"
  end
end

def check_must_requirements(files)
  errors = []

  files.each do |path|
    content_lines(path) do |line, number|
      next unless line.match?(/^\s*\|\s*REQ-\d{3,4}\s*\|/)

      cells = line.strip.delete_prefix("|").delete_suffix("|").split("|").map(&:strip)
      id = cells.first
      priority_index = cells.index { |cell| cell.downcase == "must" }
      next unless priority_index

      related_ac_text = cells[(priority_index + 1)..]&.join(" | ").to_s
      next if reference_scan_line(related_ac_text).match?(AC_ID_PATTERN)

      errors << "#{relative(path)}:#{number} must requirement #{id} has no AC link"
    end
  end

  errors
end

def check_placeholders(files)
  errors = []

  files.each do |path|
    content_lines(path) do |line, number|
      scan_line = reference_code_text(line)

      PLACEHOLDER_PATTERNS.each do |pattern|
        next unless (match = scan_line.match(pattern))

        errors << "#{relative(path)}:#{number} placeholder/template remnant: #{match[0]}"
        break
      end
    end
  end

  errors
end

all_markdown_files = markdown_files
active_files = all_markdown_files.select { |path| active_doc?(path) }
symlink_errors = reject_symlinked_markdown(active_files)
active_files = active_files.reject(&:symlink?)
definitions, references, adr_filename_errors = collect_definitions_and_references(active_files)

errors = []
errors.concat(symlink_errors)
errors.concat(adr_filename_errors)
errors.concat(check_duplicate_definitions(definitions))
errors.concat(check_dangling_references(definitions, references))
errors.concat(check_must_requirements(active_files))
errors.concat(check_placeholders(active_files)) if STRICT

if errors.empty?
  mode = STRICT ? "strict" : "default"
  puts "Doc governance check passed (#{mode} mode, #{active_files.length} active Markdown files, #{definitions.length} IDs)."
  exit 0
end

warn "Doc governance check failed:"
errors.each { |error| warn "- #{error}" }
exit 1
