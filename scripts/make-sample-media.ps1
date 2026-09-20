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
$media = Join-Path $root 'media'
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

# Audiobooks: Audiobookshelf reads Author/Title from the folder structure.
$audiobooks = @(
  @{ Path = 'audiobooks/Frank Herbert/Dune Messiah/Dune Messiah.mp3';                   Title = 'Dune Messiah';         Author = 'Frank Herbert';     Freq = 180; Year = 1969 }
  @{ Path = 'audiobooks/Ursula K. Le Guin/A Wizard of Earthsea/A Wizard of Earthsea.mp3'; Title = 'A Wizard of Earthsea'; Author = 'Ursula K. Le Guin'; Freq = 260; Year = 1968 }
)

# Ebooks are built inside the calibre-web container, because they have to be
# registered in a Calibre database rather than just dropped in a folder.
$ebooks = @(
  @{ Title = 'Dune';                 Author = 'Frank Herbert' }
  @{ Title = 'A Wizard of Earthsea'; Author = 'Ursula K. Le Guin' }
)

foreach ($item in @($tracks | ForEach-Object { $_.Path }) + $films + @($audiobooks | ForEach-Object { $_.Path })) {
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

# Ebooks need calibredb, which lives in the running calibre-web container. Skip
# rather than fail if the stack is not up - the rest of the library is still
# useful, and this can be re-run later.
$cwRunning = (& docker ps --filter 'name=atrium-calibreweb' --filter 'status=running' --format '{{.Names}}') -contains 'atrium-calibreweb'
if (-not $cwRunning) {
  Write-Host "  (skipping ebooks: start the stack first, then re-run this script)" -ForegroundColor Yellow
} else {
  foreach ($b in $ebooks) {
    Write-Host "  ebook  $($b.Title)"
    # One line, and no here-string: PowerShell here-strings carry CRLF, and a
    # stray carriage return makes bash read a trailing CR as part of the command.
    $cmd = "cd /tmp && { echo '$($b.Title)'; echo; echo 'Placeholder text for a synthetic test library.'; } > in.txt && ebook-convert in.txt out.epub --title '$($b.Title)' --authors '$($b.Author)' --language en >/dev/null 2>&1 && calibredb add --with-library /books out.epub >/dev/null 2>&1 && chown -R abc:abc /books"
    & docker exec atrium-calibreweb bash -c $cmd
  }
}

Write-Host "`nDone. Library:" -ForegroundColor Green
Get-ChildItem -Recurse -File $media | ForEach-Object {
  "  {0,8:N0} KB  {1}" -f ($_.Length / 1KB), $_.FullName.Substring($media.Length + 1)
}
