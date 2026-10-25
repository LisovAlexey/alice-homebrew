"""Add a non-blocking PCM meter to the pinned Shairport ALSA backend."""
from pathlib import Path
import sys
source = Path(sys.argv[1]) / 'audio_alsa.c'
text = source.read_text()
if 'alice_meter_pcm' not in text:
    marker = 'static int do_play(void *buf, int samples) {'
    if text.count(marker) != 1:
        raise RuntimeError('Unsupported Shairport ALSA source; no patch applied')
    text = '#include "alice_audio_meter.h"\n' + text
    text = text.replace(marker, marker + '''
  if (FORMAT_FROM_ENCODED_FORMAT(current_encoded_output_format) == SPS_FORMAT_S32_LE)
    alice_meter_pcm((const int32_t *)buf, samples,
                    CHANNELS_FROM_ENCODED_FORMAT(current_encoded_output_format),
                    RATE_FROM_ENCODED_FORMAT(current_encoded_output_format));
''', 1)
    source.write_text(text)
(source.parent / 'alice_audio_meter.h').write_bytes((Path(__file__).parent / 'audio_meter.h').read_bytes())
