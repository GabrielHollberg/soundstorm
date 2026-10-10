#!/bin/sh
# Copies the models SoundStorm's backends download on first use out of a
# working install, for box/build.sh to build into the box - so a box set up
# with no internet has them from its first start (2026-10-07, the owner's
# asking): photo search (Immich's CLIP model and its faces), "Make an ebook"
# (Whisper's base.en), and read-along syncing (Storyteller's whisper.cpp and
# its base.en).
#
# Run where Docker has a SoundStorm that has used each once - on the Windows
# PC, the live install - before box/build.sh:
#
#   sh box/models.sh            (writes box/out/models/*.tar)
#
# Settings: PROJECT=soundstorm (the compose project the volumes belong to),
# OUT=box/out. Only models are copied: nothing of anybody's media or data.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
out="${OUT:-$here/out}/models"
project="${PROJECT:-soundstorm}"
mkdir -p "$out"
# Docker on Windows wants a Windows path for the folder it writes into.
host=$(cd "$out" && { pwd -W 2>/dev/null || pwd; })
export MSYS_NO_PATHCONV=1

# export_models VOLUME NAME FOLDER PATHS...: a tar of those paths, taken from
# that folder inside the volume, owners kept as numbers (the backends run as
# their own users).
export_models() {
	vol="${project}_$1"
	name=$2
	from=$3
	shift 3
	if ! docker volume inspect "$vol" >/dev/null 2>&1; then
		echo "  $name: no volume $vol - skipped" >&2
		return
	fi
	docker run --rm -v "$vol:/m:ro" -v "$host:/out" alpine sh -c '
		name=$1; from=$2; shift 2
		cd "/m/$from" || exit 1
		for p in "$@"; do [ -e "$p" ] || { echo "  $name: missing $p" >&2; exit 1; }; done
		tar --numeric-owner -cf "/out/$name.tar" "$@"' sh "$name" "$from" "$@"
	echo "  $name: $(du -h "$out/$name.tar" | cut -f1)"
}

echo "Models into $out"
# Immich's model cache holds only models.
export_models immich-models immich-models . .
# Storyteller: the base.en model it is set to, and the whisper.cpp it runs
# (tiny.en, which it is not set to, left out).
export_models storyteller-models storyteller-models . config.json models/ggml-base.en.bin whisper-cpp
# Whisper: its model alone, from inside its folder. The rest of that cache is
# the program's own virtualenv, which Docker copies in from the image - the box
# mounts this volume at the model's folder instead (compose.box.yml).
export_models whisper-models whisper-models whisper .

# The fingerprint of every file, as box/models.sha256: box/build.sh builds in
# only models matching that list, so a change here is seen and reviewed
# (git diff box/models.sha256) before it reaches a box.
here_host=$(cd "$here" && { pwd -W 2>/dev/null || pwd; })
docker run --rm -v "$host:/out:ro" -v "$here_host:/b:ro" alpine sh -c '
	tr -d "\r" </b/models-list.sh >/tmp/list.sh
	for t in /out/*.tar; do
		n=$(basename "$t" .tar)
		mkdir -p "/tmp/m/$n" && tar -xf "$t" -C "/tmp/m/$n" && sh /tmp/list.sh -c "/tmp/m/$n" "$n" || exit 1
	done' >"$here/models.sha256.new"
mv "$here/models.sha256.new" "$here/models.sha256"
echo "Fingerprints written to box/models.sha256: review them (git diff box/models.sha256) and commit."
