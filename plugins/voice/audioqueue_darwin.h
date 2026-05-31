#pragma once

typedef struct jt_audio_recorder jt_audio_recorder_t;

int jt_audio_start(int write_fd, jt_audio_recorder_t **out_recorder);
void jt_audio_stop(jt_audio_recorder_t *recorder);
