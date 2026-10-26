# AirPlay 1.0.0-beta.3 validation

- Targeted Go checks cover shared mode defaults, explicit direct mode, invalid modes, lazy player startup, PCM forwarding, release after idle and resume, closing the player, split stereo frame boundaries, RMS/peak and frequency summaries for Music Ring.
- On an original Mini 1, two firmware aplay processes using the exact private shared ALSA config played four seconds of digital silence concurrently and both exited successfully. This checks the shared dmix route and firmware library ABI without starting Alice, capturing microphones or changing system configuration. Temporary files were removed.
- The bundled Shairport receiver and upstream dependencies are unchanged from beta.2. Shared mode selects its stdout backend, S16_LE stereo / 44100 Hz. Direct mode retains the ALSA backend and meter.
- This release does not claim a live iPhone stream and Alice voice dialogue were verified together. The owner's voice service remains intentionally disabled. Shared audio cannot repair an independently stopped voice service, broken sensor or microphone problem.
- No plugin installation or root/firmware changes were performed on the owner's speaker. Only source files, packages and signed catalogue publication are part of the update.
