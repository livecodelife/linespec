-- site-filter.lua — pandoc Lua filter for the linespec.dev content pages.
--
-- Two jobs:
--   1. Pandoc:    build the sidebar nav as a flat list of section headings and
--                 expose it to the template as the `sidebar_toc` variable. The
--                 two source docs use different heading conventions
--                 (PROVENANCE_RECORDS.md = one H1 title + H2 sections;
--                 LINESPEC.md = H1 sections), so the section level is passed in
--                 per page via `-M toc_level=N`. The document title (first
--                 heading) and the in-body "Table of Contents" are skipped.
--   2. CodeBlock: wrap every fenced code block in the site's existing
--                 ".code-block / .code-header / .copy-btn" markup so the
--                 rendered HTML matches the hand-authored pages structurally and
--                 the existing CSS (docs.css) and copy-button JS (main.js) work
--                 unchanged.

-- --- 1. Build the sidebar TOC -----------------------------------------------
function Pandoc(doc)
  local level = 2
  if doc.meta.toc_level then
    level = tonumber(pandoc.utils.stringify(doc.meta.toc_level)) or 2
  end

  local items = {}
  local first = true
  for _, blk in ipairs(doc.blocks) do
    if blk.t == "Header" then
      if first then
        first = false -- the first heading is the page title; not a nav entry
      elseif blk.level == level and blk.identifier ~= "table-of-contents" then
        local text = pandoc.utils.stringify(blk)
        table.insert(items, '<li><a href="#' .. blk.identifier .. '">' .. text .. '</a></li>')
      end
    end
  end

  local html = '<ul>\n' .. table.concat(items, "\n") .. '\n</ul>'
  doc.meta.sidebar_toc = pandoc.MetaBlocks({ pandoc.RawBlock("html", html) })
  return doc
end

-- --- 2. Wrap code blocks -----------------------------------------------------
local LABELS = {
  bash = "Terminal", sh = "Terminal", shell = "Terminal", console = "Terminal", zsh = "Terminal",
  yaml = "YAML", yml = "YAML",
  json = "JSON",
  go = "Go", golang = "Go",
  sql = "SQL",
  toml = "TOML",
  ini = "INI",
  text = "Text", txt = "Text",
}

local COPY_SVG = '<svg width="16" height="16" viewBox="0 0 16 16" fill="none">'
  .. '<path d="M4 4v8h8V4H4zM2 2h12v12H2V2z" stroke="currentColor" stroke-width="1.5"/>'
  .. '</svg>'

local function label_for(lang)
  if not lang or lang == "" then return "Code" end
  return LABELS[lang:lower()] or lang:upper()
end

local function escape_html(s)
  return (s:gsub("&", "&amp;"):gsub("<", "&lt;"):gsub(">", "&gt;"))
end

function CodeBlock(el)
  local label = label_for(el.classes[1])
  local html = '<div class="code-block">'
    .. '<div class="code-header"><span>' .. label .. '</span>'
    .. '<button class="copy-btn" aria-label="Copy code">' .. COPY_SVG .. '</button></div>'
    .. '<pre><code>' .. escape_html(el.text) .. '</code></pre>'
    .. '</div>'
  return pandoc.RawBlock("html", html)
end
