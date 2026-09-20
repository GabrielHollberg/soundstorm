# Generates a tiny synthetic library so the stack has something to find.
#
# Everything here is produced by ffmpeg from a sine wave and a test pattern, so
# there is nothing to download and nothing copyrighted. The titles are chosen so
# that searching "dune" returns a song AND a film - which is the one thing this
# whole project is for.
#
# ffmpeg comes from the jellyfin image, which is already being pulled, so there
# is no separate dependency to install.
#
#   pwsh scripts/make-sample-media.ps1

$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
$media = Join-Path $root 'library'
$image = 'jellyfin/jellyfin:latest'
$ffmpeg = '/usr/lib/jellyfin-ffmpeg/ffmpeg'

Write-Host "Writing a sample library into $media" -ForegroundColor Cyan

# Tracks: relative path, title, artist, album, tone frequency.
$tracks = @(
  @{ Path = 'music/Test Artist/Dune Suite/01 - Dune.mp3';          Title = 'Dune';          Artist = 'Test Artist'; Album = 'Dune Suite'; Freq = 440; Track = 1 }
  @{ Path = 'music/Test Artist/Dune Suite/02 - Sands of Arrakis.mp3'; Title = 'Sands of Arrakis'; Artist = 'Test Artist'; Album = 'Dune Suite'; Freq = 330; Track = 2 }
  @{ Path = 'music/Other Band/Assorted/01 - Blue Monday.mp3';      Title = 'Blue Monday';   Artist = 'Other Band';  Album = 'Assorted';   Freq = 220; Track = 1 }
)

# Films: relative path. Jellyfin reads the title and year from the folder name.
$films = @(
  'movies/Dune (2021)/Dune (2021).mp4'
  'movies/Blade Runner 2049 (2017)/Blade Runner 2049 (2017).mp4'
)

# One film the browser genuinely cannot decode: HEVC video, FLAC audio, MKV
# container - none of the three plays in Chrome. It exists so the transcoding
# path is exercised by the sample library rather than only in theory.
$hevcFilm = 'movies/Nightfall (2019)/Nightfall (2019).mkv'

# TV: Jellyfin reads show, season and episode from this exact folder shape.
$episodes = @(
  'tv/Sandworms (2021)/Season 01/Sandworms - S01E01 - The Deep Desert.mp4'
  'tv/Sandworms (2021)/Season 01/Sandworms - S01E02 - Spice Must Flow.mp4'
)

# Audiobooks: Audiobookshelf reads Author/Title from the folder structure.
$audiobooks = @(
  @{ Path = 'audiobooks/Frank Herbert/Dune Messiah/Dune Messiah.mp3';                   Title = 'Dune Messiah';         Author = 'Frank Herbert';     Freq = 180; Year = 1969 }
  @{ Path = 'audiobooks/Ursula K. Le Guin/A Wizard of Earthsea/A Wizard of Earthsea.mp3'; Title = 'A Wizard of Earthsea'; Author = 'Ursula K. Le Guin'; Freq = 260; Year = 1968 }
)

# Ebooks are built inside the calibre-web container, because they have to be
# registered in a Calibre database rather than just dropped in a folder.
$ebooks = @(
  @{ Title = 'Dune';                     Author = 'Frank Herbert';     Year = 1965 }
  @{ Title = 'A Wizard of Earthsea';     Author = 'Ursula K. Le Guin'; Year = 1968 }
  @{ Title = 'The Left Hand of Darkness'; Author = 'Ursula K. Le Guin'; Year = 1969 }
)

foreach ($item in @($tracks | ForEach-Object { $_.Path }) + $films + $episodes + @($audiobooks | ForEach-Object { $_.Path })) {
  $dir = Split-Path -Parent (Join-Path $media $item)
  if (-not (Test-Path $dir)) { New-Item -ItemType Directory -Force $dir | Out-Null }
}

# One container invocation per file. Slower than batching but far easier to read,
# and this runs once.
function Invoke-Ffmpeg {
  param([string[]]$FfmpegArgs)
  $dockerArgs = @(
    'run', '--rm',
    '-v', "$($media):/out",
    '--entrypoint', $ffmpeg,
    $image,
    '-hide_banner', '-loglevel', 'error', '-y'
  ) + $FfmpegArgs
  & docker @dockerArgs
  if ($LASTEXITCODE -ne 0) { throw "ffmpeg failed: $($FfmpegArgs -join ' ')" }
}

foreach ($t in $tracks) {
  Write-Host "  track  $($t.Title)"
  Invoke-Ffmpeg @(
    '-f', 'lavfi', '-i', "sine=frequency=$($t.Freq):duration=20",
    '-metadata', "title=$($t.Title)",
    '-metadata', "artist=$($t.Artist)",
    '-metadata', "album_artist=$($t.Artist)",
    '-metadata', "album=$($t.Album)",
    '-metadata', "track=$($t.Track)",
    '-metadata', 'date=2021',
    '-c:a', 'libmp3lame', '-q:a', '7',
    "/out/$($t.Path)"
  )
}

foreach ($f in $films) {
  Write-Host "  film   $(Split-Path -Leaf $f)"
  Invoke-Ffmpeg @(
    '-f', 'lavfi', '-i', 'testsrc=duration=15:size=640x360:rate=24',
    '-f', 'lavfi', '-i', 'sine=frequency=200:duration=15',
    # h264 + aac in mp4 is what a browser can direct-play, which keeps
    # transcoding out of this slice entirely.
    '-c:v', 'libx264', '-preset', 'veryfast', '-pix_fmt', 'yuv420p',
    '-c:a', 'aac', '-b:a', '96k',
    '-shortest', '-movflags', '+faststart',
    "/out/$f"
  )
}

# Two subtitle paths worth exercising: one embedded in the MKV, one sitting
# beside the mp4 as a sidecar. Jellyfin converts both to WebVTT on request.
$srt = Join-Path $media 'subs.srt'
@(
  '1', '00:00:01,000 --> 00:00:05,000', 'The spice must flow.', '',
  '2', '00:00:06,000 --> 00:00:11,000', 'Fear is the mind-killer.', '',
  '3', '00:00:12,000 --> 00:00:14,500', 'I must not fear.', ''
) | Set-Content -Encoding ascii $srt
Copy-Item $srt (Join-Path $media 'movies/Dune (2021)/Dune (2021).en.srt') -Force

Write-Host "  film   $(Split-Path -Leaf $hevcFilm) (HEVC/FLAC/MKV - forces a transcode)"
$dir = Split-Path -Parent (Join-Path $media $hevcFilm)
if (-not (Test-Path $dir)) { New-Item -ItemType Directory -Force $dir | Out-Null }
Invoke-Ffmpeg @(
  '-f', 'lavfi', '-i', 'testsrc=duration=20:size=640x360:rate=24',
  '-f', 'lavfi', '-i', 'sine=frequency=440:duration=20',
  '-i', '/out/subs.srt',
  '-map', '0:v', '-map', '1:a', '-map', '2:s',
  '-c:v', 'libx265', '-preset', 'ultrafast', '-pix_fmt', 'yuv420p', '-tag:v', 'hvc1',
  '-c:a', 'flac', '-c:s', 'srt',
  '-metadata:s:s:0', 'language=eng', '-metadata:s:s:0', 'title=English',
  "/out/$hevcFilm"
)
Remove-Item $srt -Force -ErrorAction SilentlyContinue

foreach ($e in $episodes) {
  Write-Host "  episode $(Split-Path -Leaf $e)"
  Invoke-Ffmpeg @(
    '-f', 'lavfi', '-i', 'testsrc=duration=10:size=640x360:rate=24',
    '-f', 'lavfi', '-i', 'sine=frequency=300:duration=10',
    '-c:v', 'libx264', '-preset', 'veryfast', '-pix_fmt', 'yuv420p',
    '-c:a', 'aac', '-b:a', '96k',
    '-shortest', '-movflags', '+faststart',
    "/out/$e"
  )
}

foreach ($a in $audiobooks) {
  Write-Host "  book   $($a.Title)"
  Invoke-Ffmpeg @(
    '-f', 'lavfi', '-i', "sine=frequency=$($a.Freq):duration=25",
    '-metadata', "title=$($a.Title)",
    '-metadata', "artist=$($a.Author)",
    '-metadata', "album_artist=$($a.Author)",
    '-metadata', "album=$($a.Title)",
    '-metadata', "date=$($a.Year)",
    '-c:a', 'libmp3lame', '-q:a', '7',
    "/out/$($a.Path)"
  )
}

# Ebooks are written directly by scripts/mkepub - an EPUB is a zip with two XML
# files, so producing one needs no Calibre and no container.
foreach ($b in $ebooks) {
  Write-Host "  ebook  $($b.Title)"
  $safe = ($b.Title -replace '[\/:*?"<>|]', '_')
  $dest = Join-Path $media "ebooks/$safe - $($b.Author).epub"
  & go run ./scripts/mkepub -out $dest -title $b.Title -author $b.Author -year $b.Year | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "mkepub failed for $($b.Title)" }
}

Write-Host "`nDone. Library:" -ForegroundColor Green
Get-ChildItem -Recurse -File $media | ForEach-Object {
  "  {0,8:N0} KB  {1}" -f ($_.Length / 1KB), $_.FullName.Substring($media.Length + 1)
}
