//go:build darwin && cgo

#include "audioqueue_darwin.h"

#include <AudioToolbox/AudioToolbox.h>
#include <CoreAudio/CoreAudioTypes.h>
#include <pthread.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#define JT_BUFFER_COUNT 3
#define JT_BUFFER_BYTES 4096

struct jt_audio_recorder {
	AudioQueueRef queue;
	AudioQueueBufferRef buffers[JT_BUFFER_COUNT];
	int write_fd;
	pthread_mutex_t mu;
	int running;
};

static void jt_audio_cb(void *user_data, AudioQueueRef queue, AudioQueueBufferRef buffer,
			const AudioTimeStamp *start_time, UInt32 packet_count,
			const AudioStreamPacketDescription *packet_desc) {
	(void)start_time;
	(void)packet_count;
	(void)packet_desc;
	jt_audio_recorder_t *rec = (jt_audio_recorder_t *)user_data;

	pthread_mutex_lock(&rec->mu);
	int running = rec->running;
	int fd = rec->write_fd;
	pthread_mutex_unlock(&rec->mu);

	if (running && fd >= 0 && buffer->mAudioDataByteSize > 0) {
		const char *p = (const char *)buffer->mAudioData;
		UInt32 left = buffer->mAudioDataByteSize;
		while (left > 0) {
			ssize_t n = write(fd, p, left);
			if (n <= 0) break;
			p += n;
			left -= (UInt32)n;
		}
	}

	if (running) {
		AudioQueueEnqueueBuffer(queue, buffer, 0, NULL);
	}
}

int jt_audio_start(int write_fd, jt_audio_recorder_t **out_recorder) {
	if (out_recorder == NULL) return -1;
	*out_recorder = NULL;

	jt_audio_recorder_t *rec = (jt_audio_recorder_t *)calloc(1, sizeof(jt_audio_recorder_t));
	if (rec == NULL) return -1;
	rec->write_fd = write_fd;
	rec->running = 1;
	pthread_mutex_init(&rec->mu, NULL);

	AudioStreamBasicDescription fmt;
	memset(&fmt, 0, sizeof(fmt));
	fmt.mSampleRate = 16000.0;
	fmt.mFormatID = kAudioFormatLinearPCM;
	fmt.mFormatFlags = kLinearPCMFormatFlagIsSignedInteger | kLinearPCMFormatFlagIsPacked;
	fmt.mBitsPerChannel = 16;
	fmt.mChannelsPerFrame = 1;
	fmt.mFramesPerPacket = 1;
	fmt.mBytesPerFrame = 2;
	fmt.mBytesPerPacket = 2;

	OSStatus err = AudioQueueNewInput(&fmt, jt_audio_cb, rec, NULL, kCFRunLoopCommonModes, 0, &rec->queue);
	if (err != noErr) {
		pthread_mutex_destroy(&rec->mu);
		free(rec);
		return (int)err;
	}

	for (int i = 0; i < JT_BUFFER_COUNT; i++) {
		err = AudioQueueAllocateBuffer(rec->queue, JT_BUFFER_BYTES, &rec->buffers[i]);
		if (err != noErr) {
			AudioQueueDispose(rec->queue, true);
			pthread_mutex_destroy(&rec->mu);
			free(rec);
			return (int)err;
		}
		err = AudioQueueEnqueueBuffer(rec->queue, rec->buffers[i], 0, NULL);
		if (err != noErr) {
			AudioQueueDispose(rec->queue, true);
			pthread_mutex_destroy(&rec->mu);
			free(rec);
			return (int)err;
		}
	}

	err = AudioQueueStart(rec->queue, NULL);
	if (err != noErr) {
		AudioQueueDispose(rec->queue, true);
		pthread_mutex_destroy(&rec->mu);
		free(rec);
		return (int)err;
	}

	*out_recorder = rec;
	return 0;
}

void jt_audio_stop(jt_audio_recorder_t *rec) {
	if (rec == NULL) return;
	pthread_mutex_lock(&rec->mu);
	rec->running = 0;
	int fd = rec->write_fd;
	rec->write_fd = -1;
	pthread_mutex_unlock(&rec->mu);

	if (rec->queue != NULL) {
		AudioQueueStop(rec->queue, true);
		AudioQueueDispose(rec->queue, true);
	}
	if (fd >= 0) {
		close(fd);
	}
	pthread_mutex_destroy(&rec->mu);
	free(rec);
}
