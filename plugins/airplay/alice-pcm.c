/* Paced PCM writer for the Alice firmware ALSA library. MIT, Alice Homebrew contributors.
 *
 * The firmware aplay is a 32-bit process on an arm64 kernel. Through dmix every
 * pointer sync is a compat ioctl that reads the Amlogic audio registers, and
 * aplay's poll loop never sleeps, so it burns a whole core and only plays about
 * 68% of real time. This writer uses non-blocking writes and sleeps while the
 * mixer buffer is full, keeping the stock library and its dmix ABI.
 *
 * Usage: alice-pcm DEVICE RATE CHANNELS   (raw S16_LE on stdin)
 */
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <unistd.h>

/* Minimal ALSA declarations: the firmware ships the library without headers. */
typedef struct _snd_pcm snd_pcm_t;
typedef unsigned long snd_pcm_uframes_t;
typedef long snd_pcm_sframes_t;
#define SND_PCM_STREAM_PLAYBACK 0
#define SND_PCM_NONBLOCK 1
#define SND_PCM_FORMAT_S16_LE 2
#define SND_PCM_ACCESS_RW_INTERLEAVED 3
int snd_pcm_open(snd_pcm_t **pcm, const char *name, int stream, int mode);
int snd_pcm_set_params(snd_pcm_t *pcm, int format, int access, unsigned int channels,
                       unsigned int rate, int soft_resample, unsigned int latency);
int snd_pcm_get_params(snd_pcm_t *pcm, snd_pcm_uframes_t *buffer_size, snd_pcm_uframes_t *period_size);
snd_pcm_sframes_t snd_pcm_writei(snd_pcm_t *pcm, const void *buffer, snd_pcm_uframes_t size);
snd_pcm_sframes_t snd_pcm_avail(snd_pcm_t *pcm);
int snd_pcm_recover(snd_pcm_t *pcm, int err, int silent);
int snd_pcm_close(snd_pcm_t *pcm);
const char *snd_strerror(int errnum);

#define LATENCY_US 200000
#define CHUNK_FRAMES 1024

static void pause_ms(long ms) {
    struct timespec ts = {ms / 1000, (ms % 1000) * 1000000L};
    while (nanosleep(&ts, &ts) < 0 && errno == EINTR) {}
}

static int play(snd_pcm_t *pcm, const unsigned char *p, snd_pcm_uframes_t frames, unsigned int frame_bytes) {
    while (frames > 0) {
        snd_pcm_sframes_t n = snd_pcm_writei(pcm, p, frames);
        if (n == -EAGAIN || n == 0) {
            pause_ms(5);
            continue;
        }
        if (n < 0) {
            int err = snd_pcm_recover(pcm, (int)n, 1);
            if (err < 0) {
                fprintf(stderr, "alice-pcm: write: %s\n", snd_strerror(err));
                return -1;
            }
            continue;
        }
        p += (size_t)n * frame_bytes;
        frames -= (snd_pcm_uframes_t)n;
    }
    return 0;
}

int main(int argc, char **argv) {
    if (argc != 4) {
        fprintf(stderr, "usage: alice-pcm DEVICE RATE CHANNELS < S16_LE\n");
        return 2;
    }
    unsigned int rate = (unsigned int)strtoul(argv[2], NULL, 10);
    unsigned int channels = (unsigned int)strtoul(argv[3], NULL, 10);
    if (rate < 8000 || rate > 192000 || channels < 1 || channels > 8) {
        fprintf(stderr, "alice-pcm: bad rate or channels\n");
        return 2;
    }
    snd_pcm_t *pcm;
    int err = snd_pcm_open(&pcm, argv[1], SND_PCM_STREAM_PLAYBACK, SND_PCM_NONBLOCK);
    if (err < 0) {
        fprintf(stderr, "alice-pcm: open %s: %s\n", argv[1], snd_strerror(err));
        return 1;
    }
    err = snd_pcm_set_params(pcm, SND_PCM_FORMAT_S16_LE, SND_PCM_ACCESS_RW_INTERLEAVED, channels, rate, 1, LATENCY_US);
    if (err < 0) {
        fprintf(stderr, "alice-pcm: params: %s\n", snd_strerror(err));
        snd_pcm_close(pcm);
        return 1;
    }
    snd_pcm_uframes_t buffer_size = 0, period_size = 0;
    snd_pcm_get_params(pcm, &buffer_size, &period_size);

    const unsigned int frame_bytes = 2 * channels;
    static unsigned char buf[CHUNK_FRAMES * 2 * 8];
    size_t have = 0;
    int status = 0;
    for (;;) {
        ssize_t r = read(0, buf + have, CHUNK_FRAMES * frame_bytes - have);
        if (r < 0 && errno == EINTR)
            continue;
        if (r <= 0)
            break;
        have += (size_t)r;
        size_t whole = have - have % frame_bytes;
        if (whole == 0)
            continue;
        if (play(pcm, buf, whole / frame_bytes, frame_bytes) < 0) {
            status = 1;
            break;
        }
        memmove(buf, buf + whole, have - whole);
        have -= whole;
    }
    /* Let queued audio finish without a blocking drain; give up after a second. */
    for (int i = 0; status == 0 && buffer_size > 0 && i < 100; i++) {
        snd_pcm_sframes_t avail = snd_pcm_avail(pcm);
        if (avail < 0 || (snd_pcm_uframes_t)avail + period_size >= buffer_size)
            break;
        pause_ms(10);
    }
    snd_pcm_close(pcm);
    return status;
}
