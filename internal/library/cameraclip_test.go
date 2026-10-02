package library

import "testing"

func TestCameraClipNames(t *testing.T) {
	for _, name := range []string{
		"MVI_0002.MOV", "PXL_20230301_101010.mp4", "C0001.MP4", "P1000123.MOV",
		"PRIVATE/AVCHD/BDMV/STREAM/00001.MTS", "00012.m2ts", "RPReplay_Final1605.MP4",
		"Screen Recording 2024-01-01 at 10.00.00.mov", "Screen_Recording_20240101-101010.mp4",
	} {
		if !looksLikeCameraClip(name) {
			t.Errorf("%s should look like a camera's clip", name)
		}
	}
	for _, name := range []string{"Dune (2021).mkv", "Casablanca.mp4", "Cars 2.mp4", "Coco.mp4"} {
		if looksLikeCameraClip(name) {
			t.Errorf("%s is a film", name)
		}
	}
}
