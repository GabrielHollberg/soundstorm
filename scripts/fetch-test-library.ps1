# Builds a test library out of real media that is free to redistribute.
#
# The synthetic library from make-sample-media.ps1 proves each mechanism works.
# It cannot prove they survive contact with real files, because every file in it
# was produced by us and therefore has exactly the metadata we chose to write.
# Real files are inconsistent in ways nobody predicts: missing years, multiple
# creators, unicode, empty tags, titles that are filenames.
#
# Everything here is public domain or Creative Commons:
#
#   ebooks      Project Gutenberg
#   audiobooks  LibriVox (public domain recordings of public domain books)
#   music       Internet Archive etree collection (trade-friendly live concerts)
#   films       Blender Foundation open movies, via the Internet Archive
#
# It is resumable: anything already downloaded is skipped, so a failed run can
# just be repeated.
#
#   pwsh scripts/fetch-test-library.ps1
#   pwsh scripts/fetch-test-library.ps1 -Ebooks 500 -Concerts 4

param(
  [int]$Ebooks = 250,
  [int]$Audiobooks = 3,
  [int]$Concerts = 2,
  [switch]$SkipFilms,
  [string]$Root = (Join-Path (Split-Path -Parent $PSScriptRoot) 'library'),

  # A mirror, never gutenberg.org - see the ebooks section for why.
  # Alternatives are listed at https://www.gutenberg.org/MIRRORS.ALL
  [string]$GutenbergMirror = 'https://gutenberg.pglaf.org',
  [int]$GutenbergDelayMs = 2000
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'   # the progress bar makes downloads far slower

function Write-Step { param([string]$Text) Write-Host "`n$Text" -ForegroundColor Cyan }
function Write-Item { param([string]$Text) Write-Host "  $Text" }

# Strips the characters Windows will not accept in a path.
function Get-SafeName {
  param([string]$Name, [int]$Max = 80)
  $safe = ($Name -replace '[\\/:*?"<>|]', '-').Trim()
  $safe = $safe -replace '\s+', ' '
  if ($safe.Length -gt $Max) { $safe = $safe.Substring(0, $Max).Trim() }
  if (-not $safe) { $safe = 'Unknown' }
  return $safe
}

function Get-File {
  param([string]$Url, [string]$Destination)
  if (Test-Path $Destination) { return $true }
  $dir = Split-Path -Parent $Destination
  if (-not (Test-Path $dir)) { New-Item -ItemType Directory -Force $dir | Out-Null }

  $tmp = "$Destination.part"
  try {
    Invoke-WebRequest -Uri $Url -OutFile $tmp -TimeoutSec 300 -MaximumRedirection 5 `
      -UserAgent 'SoundStorm test-library fetcher'
    Move-Item -Force $tmp $Destination
    return $true
  } catch {
    Remove-Item $tmp -Force -ErrorAction SilentlyContinue
    return $false
  }
}

Write-Host "Building a real test library in $Root" -ForegroundColor Green

# --- Films ------------------------------------------------------------------
# The Blender open movies: genuinely produced films, with real encodes rather
# than a test pattern, and permissively licensed.
if (-not $SkipFilms) {
  Write-Step 'Films (Blender open movies, CC-BY)'
  $films = @(
    @{ Title = 'Big Buck Bunny';  Year = 2008; Url = 'https://archive.org/download/BigBuckBunny_124/Content/big_buck_bunny_720p_surround.mp4'; Ext = 'mp4' }
    @{ Title = 'Sintel';          Year = 2010; Url = 'https://archive.org/download/Sintel/sintel-2048-surround_512kb.mp4';                     Ext = 'mp4' }
    @{ Title = 'Elephants Dream'; Year = 2006; Url = 'https://archive.org/download/ElephantsDream/ed_1024_512kb.mp4';                          Ext = 'mp4' }
  )
  foreach ($f in $films) {
    $name = "$($f.Title) ($($f.Year))"
    $dest = Join-Path $Root "movies/$name/$name.$($f.Ext)"
    if (Test-Path $dest) { Write-Item "$name (already have it)"; continue }
    Write-Item "$name ..."
    if (Get-File -Url $f.Url -Destination $dest) {
      Write-Item "$name -> $([math]::Round((Get-Item $dest).Length / 1MB)) MB"
    } else {
      Write-Host "    failed" -ForegroundColor Yellow
    }
  }
}

# --- Ebooks -----------------------------------------------------------------
# From a Project Gutenberg MIRROR, by id, deliberately.
#
# gutenberg.org itself says: "The Project Gutenberg website is intended for
# human users only. Any perceived use of automated tools to access the Project
# Gutenberg website will result in a temporary or permanent block of your IP
# address." Their sanctioned routes for anything automated are the mirrors and
# the /robot/harvest endpoint, and their own example throttles with `wget -w 2`.
# The mirror serves byte-identical files, so this costs nothing but courtesy.
#
# No catalogue API is involved on purpose: the whole point is to feed our EPUB
# parser files whose metadata we did not write, exactly as they come.
if ($Ebooks -gt 0) {
  Write-Step "Ebooks (Project Gutenberg, up to $Ebooks)"
  $dir = Join-Path $Root 'ebooks'
  if (-not (Test-Path $dir)) { New-Item -ItemType Directory -Force $dir | Out-Null }

  $got = 0
  $id = 1
  $attempts = 0
  while ($got -lt $Ebooks -and $attempts -lt ($Ebooks * 3)) {
    $attempts++
    $dest = Join-Path $dir "pg$id.epub"
    if (Test-Path $dest) { $got++; $id++; continue }

    if (Get-File -Url "$GutenbergMirror/cache/epub/$id/pg$id.epub" -Destination $dest) {
      # Not every id has an EPUB, and a miss can still return a small HTML page.
      $size = (Get-Item $dest).Length
      if ($size -lt 2048) {
        Remove-Item $dest -Force
      } else {
        $got++
        if ($got % 25 -eq 0) { Write-Item "$got books" }
      }
    }
    $id++
    # Project Gutenberg is a donated service and their own guidance throttles
    # at two seconds. A slow one-off build is a fair price for not being the
    # reason they start blocking people.
    Start-Sleep -Milliseconds $GutenbergDelayMs
  }
  Write-Item "$got books in $dir"
}

# --- Audiobooks -------------------------------------------------------------
# LibriVox ships each book as a zip of per-chapter MP3s, which is the multi-file
# shape real audiobooks have and our synthetic single-file ones never did.
if ($Audiobooks -gt 0) {
  Write-Step "Audiobooks (LibriVox, $Audiobooks titles)"
  $feed = Invoke-RestMethod -TimeoutSec 60 -UserAgent 'SoundStorm test-library fetcher' `
    -Uri 'https://librivox.org/api/feed/audiobooks/?format=json&limit=60&fields=id,title,authors,url_zip_file,totaltimesecs'

  # Short books only: a fifty-hour Count of Monte Cristo is not a better test
  # than a two-hour one, it is just a slower download.
  $picked = $feed.books |
    Where-Object { $_.url_zip_file -and [int]$_.totaltimesecs -gt 0 -and [int]$_.totaltimesecs -lt 9000 } |
    Select-Object -First $Audiobooks

  foreach ($book in $picked) {
    $author = if ($book.authors -and $book.authors[0].last_name) {
      Get-SafeName "$($book.authors[0].first_name) $($book.authors[0].last_name)"
    } else { 'Unknown Author' }
    $title = Get-SafeName $book.title
    $dir = Join-Path $Root "audiobooks/$author/$title"

    if ((Test-Path $dir) -and (Get-ChildItem $dir -Filter *.mp3 -ErrorAction SilentlyContinue)) {
      Write-Item "$title (already have it)"
      continue
    }

    Write-Item "$title by $author ($([math]::Round([int]$book.totaltimesecs / 60)) min) ..."
    $zip = Join-Path $env:TEMP "librivox-$($book.id).zip"
    if (-not (Get-File -Url $book.url_zip_file -Destination $zip)) {
      Write-Host "    download failed" -ForegroundColor Yellow
      continue
    }
    New-Item -ItemType Directory -Force $dir | Out-Null
    try {
      Expand-Archive -Path $zip -DestinationPath $dir -Force
      $count = (Get-ChildItem $dir -Filter *.mp3 -Recurse).Count
      Write-Item "$title -> $count chapters"
    } catch {
      Write-Host "    could not extract: $_" -ForegroundColor Yellow
    } finally {
      Remove-Item $zip -Force -ErrorAction SilentlyContinue
    }
  }
}

# --- Music ------------------------------------------------------------------
# The etree collection is audience recordings of bands that allow taping. The
# tagging is gloriously inconsistent, which is the point: this is the kind of
# library Navidrome is actually good at and our synthetic one never tested.
if ($Concerts -gt 0) {
  Write-Step "Music (Internet Archive etree, $Concerts concerts)"
  $search = Invoke-RestMethod -TimeoutSec 60 -UserAgent 'SoundStorm test-library fetcher' `
    -Uri "https://archive.org/advancedsearch.php?q=collection%3Aetree+AND+format%3A%22VBR+MP3%22&fl%5B%5D=identifier&fl%5B%5D=title&fl%5B%5D=creator&rows=$($Concerts * 3)&output=json"

  $taken = 0
  foreach ($doc in $search.response.docs) {
    if ($taken -ge $Concerts) { break }

    $meta = try {
      Invoke-RestMethod -TimeoutSec 60 -Uri "https://archive.org/metadata/$($doc.identifier)"
    } catch { $null }
    if (-not $meta) { continue }

    $tracks = @($meta.files | Where-Object { $_.format -eq 'VBR MP3' } | Select-Object -First 12)
    if ($tracks.Count -eq 0) { continue }

    $artist = Get-SafeName ($doc.creator | Select-Object -First 1)
    $album = Get-SafeName $doc.title 70
    $dir = Join-Path $Root "music/$artist/$album"

    if ((Test-Path $dir) -and (Get-ChildItem $dir -Filter *.mp3 -ErrorAction SilentlyContinue)) {
      Write-Item "$album (already have it)"
      $taken++
      continue
    }

    Write-Item "$artist - $album ($($tracks.Count) tracks) ..."
    $ok = 0
    foreach ($track in $tracks) {
      $dest = Join-Path $dir (Get-SafeName $track.name 90)
      if (Get-File -Url "https://archive.org/download/$($doc.identifier)/$([uri]::EscapeDataString($track.name))" -Destination $dest) { $ok++ }
    }
    Write-Item "  -> $ok tracks"
    if ($ok -gt 0) { $taken++ }
  }
}

# --- Summary ----------------------------------------------------------------
Write-Step 'Library now contains'
foreach ($folder in 'music', 'movies', 'tv', 'audiobooks', 'ebooks') {
  $path = Join-Path $Root $folder
  if (-not (Test-Path $path)) { continue }
  $files = Get-ChildItem $path -Recurse -File -ErrorAction SilentlyContinue |
    Where-Object { $_.Name -ne 'README.txt' }
  $size = ($files | Measure-Object -Property Length -Sum).Sum
  "  {0,-12} {1,6} files  {2,8:N0} MB" -f $folder, $files.Count, ($size / 1MB)
}
