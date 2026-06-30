#include <stdlib.h>
#include <unistd.h>
#include <stdio.h>
#include <string.h>

#include <signal.h>

#include <errno.h>
#include <stdint.h>

#include <sys/types.h>
#include <sys/socket.h>
#include <sys/select.h>
#include <sys/uio.h>
#include <netinet/in.h>
#include <arpa/inet.h>

#include "cipher.c"

typedef enum {
	MSG_PUNCH = 0x41,
	MSG_P2P_RDY = 0x042,
	MSG_DRW = 0xd0,
	MSG_DRW_ACK = 0xd1,
	MSG_ALIVE = 0xe0,
	MSG_ALIVE_ACK = 0xe1,
	MSG_CLOSE = 0xf0
} MsgType;

/* magic numbers */
#define MCAM	0xf1
#define MDRW	0xd1

char *MsgTypeText[256];

#define MsgSlot(a)	MsgTypeText[a] = #a
static void init_MsgTypeText() {
	MsgSlot(MSG_PUNCH);
	MsgSlot(MSG_P2P_RDY);
	MsgSlot(MSG_DRW);
	MsgSlot(MSG_DRW_ACK);
	MsgSlot(MSG_ALIVE);
	MsgSlot(MSG_ALIVE_ACK);
	MsgSlot(MSG_CLOSE);
}

#define MAXFRAGS	32
#define FRAGSPACE	2048

char fragbuf[MAXFRAGS * FRAGSPACE];
struct iovec fragspace[MAXFRAGS+1];
struct iovec *fragarray = &fragspace[1];
int fragack[MAXFRAGS];

int udp;
int http;
int client;
int connected;

struct sockaddr local, bcast;
struct sockaddr camera;

struct sockaddr_in *ilocal = (struct sockaddr_in *)&local;
struct sockaddr_in *ibcast = (struct sockaddr_in *)&bcast;
struct sockaddr_in *icamera = (struct sockaddr_in *)&camera;

#ifdef DO_DEBUG
#define DEBUG(x, ...)	fprintf(stderr, (x), __VA_ARGS__)
#else
#define DEBUG(x, ...)
#endif

static const char probe[] = {0x9f, 0xa1, 0xee, 0xb9};	/* MSG_LAN_SEARCH, encrypted with PSK "SHIX" */
#define PROBE_PORT	32108
#define HTTP_PORT	3000

#define CMDWAIT	500000	/* 500 msec */
#define VIDWAIT 800	/* 800 usec */

int timeread(int fd, myval *val, int timeout)
{
	fd_set infds;
	struct timeval tv;
	int i;

	tv.tv_sec = 0;
	tv.tv_usec = timeout;
	FD_ZERO(&infds);
	FD_SET(fd, &infds);
	i = select(fd+1, &infds, NULL, NULL, &tv);
	if (!i)
		return 0;
	i = read(fd, val->mv_data, val->mv_size);
	return i ? i : -1;	/* 0-byte read means EOF */
}

int timerecv(int fd, myval *val, struct sockaddr *from, socklen_t *fromlen)
{
	fd_set infds;
	struct timeval tv;
	int i;

	tv.tv_sec = 0;
	tv.tv_usec = CMDWAIT;
	FD_ZERO(&infds);
	FD_SET(fd, &infds);
	i = select(fd+1, &infds, NULL, NULL, &tv);
	if (!i)
		return 0;
	return recvfrom(fd, val->mv_data, val->mv_size, 0, from, fromlen);
}

static int sent_CMDs;

int sendEnc(myval *val)
{
	encode(val);
	return sendto(udp, val->mv_data, val->mv_size, 0, &camera, sizeof(*icamera));
}

int resend(myval *val)
{
	return sendto(udp, val->mv_data, val->mv_size, 0, &camera, sizeof(*icamera));
}

int sendClose()
{
	char buf[4] = {MCAM, MSG_CLOSE, 0, 0};
	myval pkt = {sizeof(buf), buf};

	sendEnc(&pkt);

	/* send multiple to make sure it gets seen */
	resend(&pkt);
	resend(&pkt);
	connected = 0;
	sent_CMDs = 0;
	close(udp);
}

int sendCMD(myval *val)
{
	char sendbuf[1024];
	int i, len = val->mv_size + 12;
	myval pkt = {len+4, sendbuf};

	sendbuf[0] = MCAM;
	sendbuf[1] = MSG_DRW;
	sendbuf[2] = len >> 8;
	sendbuf[3] = len & 0xff;
	sendbuf[4] = MDRW;
	sendbuf[5] = 0;	/* channel */
	sendbuf[6] = sent_CMDs >> 8;
	sendbuf[7] = sent_CMDs & 0xff;
	sent_CMDs++;

	len = val->mv_size;
	sendbuf[0x08] = 0x06;
	sendbuf[0x09] = 0x0a;
	sendbuf[0x0a] = 0xa0;
	sendbuf[0x0b] = 0x80;
	sendbuf[0x0c] = len & 0xff;
	len >>= 8;
	sendbuf[0x0d] = len & 0xff;
	len >>= 8;
	sendbuf[0x0e] = len & 0xff;
	len >>= 8;
	sendbuf[0x0f] = len & 0xff;

	memcpy(sendbuf+0x10, val->mv_data, val->mv_size);
	return sendEnc(&pkt);
}

int connect_camera()
{
	int i;
	if ((udp = socket(PF_INET, SOCK_DGRAM, IPPROTO_UDP)) < 0 ) {
		perror("udp socket");
		exit(1);
	}

	i = 1;
	if (setsockopt(udp, SOL_SOCKET, SO_BROADCAST, (char *)&i, sizeof(i)) < 0) {
		perror("udp setsockopt BROADCAST");
		exit(1);
	}
	ilocal->sin_port = 0;
	if (bind(udp, &local, sizeof(*ilocal)) < 0) {
		perror("udp bind");
		exit(1);
	}
	for (i=0; i<5; i++) {
		unsigned char pktbuf[128], pkt2[128];
		myval pktval = {sizeof(pktbuf), pktbuf};
		socklen_t slen = sizeof(camera);
		int len;

		if (sendto(udp, probe, sizeof(probe), 0, &bcast, sizeof(*ibcast)) != sizeof(probe)) {
			perror("udp send broadcast");
			exit(1);
		}
		printf("sent MSG_LAN_SEARCH to %s:%d\n", inet_ntoa(ibcast->sin_addr), ntohs(ibcast->sin_port));
		if ((len = timerecv(udp, &pktval, &camera, &slen)) == 0) {
			DEBUG("%s", "timed out\n");
			continue;
		}
		pktval.mv_size = len;
		memcpy(pkt2, pktbuf, len);
		decode(&pktval);
		if (pktbuf[1] != MSG_PUNCH)
			continue;
		printf("got MSG_PUNCH from %s:%d\n", inet_ntoa(icamera->sin_addr), ntohs(icamera->sin_port));
		if (1) {
			char cambuf[32];
			int id;
			len = (pktbuf[2] << 8) | pktbuf[3];
			memcpy(cambuf, pktbuf+4, 4);
			id = (pktbuf[12] << 24) | (pktbuf[13] << 16) | (pktbuf[14] << 8) | pktbuf[15];
			pktbuf[len+4] = 0;
			sprintf(cambuf+4, "-%d-%.12s", id, pktbuf+16);
			printf("camera UID %s\n", cambuf);
		}
		if (sendto(udp, pkt2, pktval.mv_size, 0, &camera, slen) != pktval.mv_size) {
			perror("udp send MSG_PUNCH");
			exit(1);
		}
		DEBUG("%s", "sent MSG_PUNCH reply\n");
		pktval.mv_size = sizeof(pktbuf);
		if ((len = timeread(udp, &pktval, CMDWAIT)) == 0) {
			DEBUG("%s", "timed out\n");
			continue;
		}
		pktval.mv_size = len;
		decode(&pktval);
		if (pktbuf[1] == MSG_P2P_RDY) {
			connected = 1;
			printf("connected!\n");
			break;
		}
	}
	return 0;
}

static const char sendvid1[] =
	"{\"pro\":\"stream\",\"cmd\":111,\"video\":1,\"user\":\"admin\",\"pwd\":\"6666\",\"devmac\":\"0000\"}";
static const char sendaud1[] =
	"{\"pro\":\"stream\",\"cmd\":111,\"audio\":1,\"user\":\"admin\",\"pwd\":\"6666\",\"devmac\":\"0000\"}";

static const char vidmagic[] = { 0x55, 0xaa, 0x15, 0xa8, 0x03, 0x00};
static const char audmagic[] = { 0x55, 0xaa, 0x15, 0xa8, 0xaa, 0x01};

#define DELIMITER	"xxxxxxkkdkdkdkdkdk__BOUNDARY"

static const char vidpart[] = "--" DELIMITER "\r\n"
	"Content-Type: image/jpeg\r\n\r\n";

static const int adpcm_index_table[16] = {
	-1, -1, -1, -1, 2, 4, 6, 8,
	-1, -1, -1, -1, 2, 4, 6, 8
};

static const int adpcm_step_table[89] = {
	7, 8, 9, 10, 11, 12, 13, 14, 16, 17,
	19, 21, 23, 25, 28, 31, 34, 37, 41, 45,
	50, 55, 60, 66, 73, 80, 88, 97, 107, 118,
	130, 143, 157, 173, 190, 209, 230, 253, 279, 307,
	337, 371, 408, 449, 494, 544, 598, 658, 724, 796,
	876, 963, 1060, 1166, 1282, 1411, 1552, 1707, 1878, 2066,
	2272, 2499, 2749, 3024, 3327, 3660, 4026, 4428, 4871, 5358,
	5894, 6484, 7132, 7845, 8630, 9493, 10442, 11487, 12635, 13899,
	15289, 16818, 18500, 20350, 22385, 24623, 27086, 29794, 32767
};

static int adpcm_index;
static int adpcm_valpred;

static void adpcm_reset()
{
	adpcm_index = 0;
	adpcm_valpred = 0;
}

static int adpcm_decode(const unsigned char *in, int inlen, unsigned char *out, int outcap)
{
	int inp = 0, outp = 0;
	int inputbuffer = 0;
	int bufferstep = 0;
	int len = inlen * 2;

	while (len-- > 0 && outp + 2 <= outcap) {
		int delta, sign, step, vpdiff;

		if (bufferstep) {
			delta = inputbuffer & 0x0f;
		} else {
			inputbuffer = in[inp++];
			delta = (inputbuffer >> 4) & 0x0f;
		}
		bufferstep = !bufferstep;

		adpcm_index += adpcm_index_table[delta];
		if (adpcm_index < 0)
			adpcm_index = 0;
		if (adpcm_index > 88)
			adpcm_index = 88;

		sign = delta & 8;
		delta &= 7;
		step = adpcm_step_table[adpcm_index];

		vpdiff = step >> 3;
		if (delta & 4)
			vpdiff += step;
		if (delta & 2)
			vpdiff += step >> 1;
		if (delta & 1)
			vpdiff += step >> 2;

		if (sign)
			adpcm_valpred -= vpdiff;
		else
			adpcm_valpred += vpdiff;

		if (adpcm_valpred > 32767)
			adpcm_valpred = 32767;
		else if (adpcm_valpred < -32768)
			adpcm_valpred = -32768;

		out[outp++] = adpcm_valpred & 0xff;
		out[outp++] = (adpcm_valpred >> 8) & 0xff;
	}

	return outp;
}

static void wav_u16(unsigned char *p, unsigned v)
{
	p[0] = v & 0xff;
	p[1] = (v >> 8) & 0xff;
}

static void wav_u32(unsigned char *p, unsigned v)
{
	p[0] = v & 0xff;
	p[1] = (v >> 8) & 0xff;
	p[2] = (v >> 16) & 0xff;
	p[3] = (v >> 24) & 0xff;
}

static void write_wav_header(int fd)
{
	unsigned char h[44];
	memcpy(h + 0, "RIFF", 4);
	wav_u32(h + 4, 0xffffffffu);
	memcpy(h + 8, "WAVE", 4);
	memcpy(h + 12, "fmt ", 4);
	wav_u32(h + 16, 16);
	wav_u16(h + 20, 1);       /* PCM */
	wav_u16(h + 22, 1);       /* mono */
	wav_u32(h + 24, 8000);    /* Hz */
	wav_u32(h + 28, 16000);   /* byte rate */
	wav_u16(h + 32, 2);       /* block align */
	wav_u16(h + 34, 16);      /* bits/sample */
	memcpy(h + 36, "data", 4);
	wav_u32(h + 40, 0xffffffffu);
	!write(fd, h, sizeof(h));
}

int send_video()
{
	unsigned char pktbuf[1280];
	myval cmd = {sizeof(sendvid1)-1, (char *)sendvid1};
	myval pkt = {sizeof(pktbuf), pktbuf};
	char repbuf[10] = {MCAM, MSG_DRW_ACK, 0, 6, MDRW};
	myval rep = {sizeof(repbuf), repbuf};
	int i, len;
	int framelen;
	int baseindex = 0;
	int slotindex;
	int datasum;
	int channel = 0;
	int nfrags = 0, ntimeout = 0;
	int rc = 0;
	unsigned short index, previndex = 0xffff;

	sendCMD(&cmd);

	if ((len = timeread(udp, &pkt, CMDWAIT)) == 0) {
		fprintf(stderr, "timed out on send video CMD\n");
		rc = -1;
		goto leave;
	}

	while(1) {
		pkt.mv_size = sizeof(pktbuf);
		len = timeread(udp, &pkt, VIDWAIT);
		if (len == 0) {
			int acks = 0;
			if (nfrags) {
				int j = nfrags;
				int ix;

				for (i=0, ix=baseindex; i<MAXFRAGS; i++, ix++) {
					if (fragarray[i].iov_len) {
						if (!fragack[i]) {
							repbuf[0] = MCAM;
							repbuf[1] = MSG_DRW_ACK;
							repbuf[2] = 0;
							repbuf[3] = 6;
							repbuf[4] = MDRW;
							repbuf[5] = channel;
							repbuf[6] = 0;
							repbuf[7] = 1;
							repbuf[8] = ix >> 8;
							repbuf[9] = ix & 0xff;
							sendEnc(&rep);
							usleep(100);
							resend(&rep);
							fragack[i] = 1;
							acks++;
						}
						j--;
						if (!j)
							break;
					}
				}
			}
			if (!acks) {
				ntimeout++;
				if (ntimeout > 1000) {
					fprintf(stderr, "timed out reading camera data\n");
					rc = -2;
					break;
				}
			}
			continue;
		}
		ntimeout = 0;
		decode(&pkt);
		if (pktbuf[0] != MCAM)
			continue;
		if (pktbuf[1] == MSG_DRW) {
			int pktoff = 8;
			len = (pktbuf[2] << 8) | pktbuf[3];
			channel = pktbuf[5];
			index = (pktbuf[6] << 8 ) | pktbuf[7];
			repbuf[0] = MCAM;
			repbuf[1] = MSG_DRW_ACK;
			repbuf[2] = 0;
			repbuf[3] = 6;
			repbuf[4] = MDRW;
			repbuf[5] = channel;
			repbuf[6] = 0;
			repbuf[7] = 1;
			repbuf[8] = index >> 8;
			repbuf[9] = index & 0xff;

			if (index != (unsigned short)(previndex + 1)) {
				if (index <= previndex || (index > 65400 && previndex < 32)) {
					DEBUG("index: %d, previndex: %d, skipping\n", index, previndex);
					sendEnc(&rep);
					usleep(100);
					resend(&rep);
					continue;
				}
				datasum = 0;
				framelen = 65535;
			}

			if (!memcmp(pktbuf+8, vidmagic, sizeof(vidmagic))) {
				/* start of video frame */
				baseindex = index;
				framelen = pktbuf[24] | (pktbuf[25] << 8) | (pktbuf[26] << 16) | (pktbuf[27] << 24);
				datasum = 0;
				pktoff = 40;
				len -= 32;
				DEBUG("index: %d start of frame, framelen %d\n", index, framelen);
				for (i=0; i<MAXFRAGS; i++) {
					fragarray[i].iov_len = 0;
					fragack[i] = 0;
				}
				nfrags = 0;
			}
			DEBUG("index: %d, pktoff: %d, len: %d\n", index, pktoff, len);
			slotindex = index - baseindex;
			if (slotindex < 0)
				slotindex += 65536;
			previndex = index;
			if (slotindex >= MAXFRAGS) {
				sendEnc(&rep);
				continue;
			}

			len -= 4;
			if (len > FRAGSPACE) {	/* data portion should always be less than 1024 bytes */
				fprintf(stderr, "packet length %d is too large, quitting\n", len);
				rc = -3;
				break;
			}
			if (!fragarray[slotindex].iov_len) {
				datasum += len;
				nfrags++;
				DEBUG("index: %d, slotindex: %d, datasum %d\n", index, slotindex, datasum);
				memcpy(fragarray[slotindex].iov_base, pktbuf+pktoff, len);
				fragarray[slotindex].iov_len = len;
			}

			if (datasum >= framelen) {
				int j = nfrags;
				int ix;
				/* frame is complete */
				DEBUG("index: %d, datasum: %d, framelen: %d, writing http\n", index, datasum, framelen);
				if (writev(client, fragspace, nfrags+1) < 0) {
					perror("writev to client");
					rc = -4;
					break;
				}

				for (i=0, ix=baseindex; i<MAXFRAGS; i++, ix++) {
					if (fragarray[i].iov_len) {
						if (!fragack[i]) {
							repbuf[0] = MCAM;
							repbuf[1] = MSG_DRW_ACK;
							repbuf[2] = 0;
							repbuf[3] = 6;
							repbuf[4] = MDRW;
							repbuf[5] = channel;
							repbuf[6] = 0;
							repbuf[7] = 1;
							repbuf[8] = ix >> 8;
							repbuf[9] = ix & 0xff;
							sendEnc(&rep);
							usleep(100);
							resend(&rep);
							fragack[i] = 1;
						}
						j--;
						if (!j)
							break;
					}
				}
			}
			pkt.mv_size = sizeof(pktbuf);
			if (timeread(client, &pkt, 0) < 0) {
				fprintf(stderr, "client closed connection\n");
				break;
			}
		}
		else if (pktbuf[1] == MSG_ALIVE) {
			pktbuf[1] = MSG_ALIVE_ACK;
			pktbuf[2] = 0;
			pktbuf[3] = 0;
			pkt.mv_size = 4;
			sendEnc(&pkt);
		}
	}
leave:
	sendClose();
	return rc;
}

int send_audio()
{
	unsigned char pktbuf[1280], pcmbuf[FRAGSPACE * 2];
	myval cmd = {sizeof(sendaud1)-1, (char *)sendaud1};
	myval pkt = {sizeof(pktbuf), pktbuf};
	char repbuf[10] = {MCAM, MSG_DRW_ACK, 0, 6, MDRW};
	myval rep = {sizeof(repbuf), repbuf};
	int len, ntimeout = 0, rc = 0;

	adpcm_reset();
	sendCMD(&cmd);

	while (1) {
		pkt.mv_size = sizeof(pktbuf);
		len = timeread(udp, &pkt, CMDWAIT);
		if (len == 0) {
			ntimeout++;
			if (ntimeout > 120) {
				fprintf(stderr, "timed out reading camera audio\n");
				rc = -1;
				break;
			}
			continue;
		}
		ntimeout = 0;
		decode(&pkt);
		if (pktbuf[0] != MCAM)
			continue;

		if (pktbuf[1] == MSG_DRW) {
			int plen = (pktbuf[2] << 8) | pktbuf[3];
			int channel = pktbuf[5];
			unsigned short index = (pktbuf[6] << 8 ) | pktbuf[7];
			int pktoff = 8;
			int rawlen;

			repbuf[0] = MCAM;
			repbuf[1] = MSG_DRW_ACK;
			repbuf[2] = 0;
			repbuf[3] = 6;
			repbuf[4] = MDRW;
			repbuf[5] = channel;
			repbuf[6] = 0;
			repbuf[7] = 1;
			repbuf[8] = index >> 8;
			repbuf[9] = index & 0xff;
			sendEnc(&rep);

			if (channel == 0) {
				if (plen > 12 && !memcmp(pktbuf+8, "\x06\x0a\xa0\x80", 4))
					printf("audio cmd response: %.*s\n", plen - 12, pktbuf + 16);
				continue;
			}
			if (channel != 2)
				continue;

			if (plen < 4)
				continue;
			rawlen = plen - 4;
			if (rawlen >= (int)sizeof(audmagic) && !memcmp(pktbuf+8, audmagic, sizeof(audmagic))) {
				pktoff = 40;
				rawlen -= 32;
			}
			if (rawlen <= 0 || rawlen > FRAGSPACE)
				continue;

			len = adpcm_decode(pktbuf + pktoff, rawlen, pcmbuf, sizeof(pcmbuf));
			if (len > 0 && write(client, pcmbuf, len) < 0) {
				perror("write audio to client");
				rc = -2;
				break;
			}

			pkt.mv_size = sizeof(pktbuf);
			if (timeread(client, &pkt, 0) < 0) {
				fprintf(stderr, "audio client closed connection\n");
				break;
			}
		}
		else if (pktbuf[1] == MSG_ALIVE) {
			pktbuf[1] = MSG_ALIVE_ACK;
			pktbuf[2] = 0;
			pktbuf[3] = 0;
			pkt.mv_size = 4;
			sendEnc(&pkt);
		}
	}
leave:
	sendClose();
	return rc;
}

static const char http200[] =
	"HTTP/1.1 200 OK\r\n"
	"Content-Type: text/html\r\n"
	"Connection: keep-alive\r\n"
	"Keep-Alive: timeout=5\r\n"
	"\r\n"
	"<!DOCTYPE html>\r\n"
	"<html><head></head><body><img src=\"/v.mjpg\"><br><audio controls src=\"/a.wav\"></audio></body></html>\r\n";

static const char http200b[] =
	"HTTP/1.1 200 OK\r\n"
	"Content-Type: multipart/x-mixed-replace; boundary=\"" DELIMITER "\"\r\n"
	"Connection: keep-alive\r\n"
	"Keep-Alive: timeout=5\r\n"
	"\r\n";

static const char http200audio[] =
	"HTTP/1.1 200 OK\r\n"
	"Content-Type: audio/wav\r\n"
	"Connection: keep-alive\r\n"
	"Keep-Alive: timeout=5\r\n"
	"\r\n";

int startup()
{
	struct timeval tv;
	struct sockaddr clientip;
	struct sockaddr_in *iclientip = (struct sockaddr_in *)&clientip;
	socklen_t clientiplen;
	int len;
	char inbuf[1024];

	if ((http = socket(PF_INET, SOCK_STREAM, IPPROTO_TCP)) < 0) {
		perror("http socket");
		exit(1);
	}
	len = 1;
	if (setsockopt(http, SOL_SOCKET, SO_REUSEADDR, (char *)&len, sizeof(len)) < 0) {
		perror("http setsockopt REUSEADDR");
		exit(1);
	}
	ilocal->sin_family = AF_INET;
	if (bind(http, &local, sizeof(*ilocal)) < 0) {
		perror("http bind");
		exit(1);
	}
	if (listen(http, 1) < 0) {
		perror("http listen");
		exit(1);
	}
again:
	clientiplen = sizeof(clientip);
	client = accept(http, &clientip, &clientiplen);
	if (client < 0) {
		perror("http accept");
		exit(1);
	}
	printf("HTTP connection from %s:%d\n",
		inet_ntoa(iclientip->sin_addr), ntohs(iclientip->sin_port));
	while (1) {
		len = read(client, inbuf, sizeof(inbuf));
		if (len < 7) {
			close(client);
			goto again;
		}
		if (strncasecmp(inbuf, "GET ", 4)) {
			close(client);
			goto again;
		}
		if (!strncmp(inbuf+4, "/v.mjpg ", 8)) {
			if (!connected)
				connect_camera(&local, &bcast);
			!write(client, http200b, sizeof(http200b)-1);
			send_video();	/* only returns on failures */
			close(client);
			goto again;
		} else
		if (!strncmp(inbuf+4, "/a.wav ", 7)) {
			if (!connected)
				connect_camera(&local, &bcast);
			!write(client, http200audio, sizeof(http200audio)-1);
			write_wav_header(client);
			send_audio();	/* only returns on failures */
			close(client);
			goto again;
		} else
		if (!strncmp(inbuf+4, "/ ", 2)) {
			!write(client, http200, sizeof(http200)-1);
			connect_camera(&local, &bcast);
			continue;
		}
	}
}

static void sighandle(int sig)
{
	if (connected)
		sendClose();
	_exit(0);
}

static void usage(char *prog)
{
	fprintf(stderr, "usage: %s [-b <broadcast addr>] [-l <local addr>] [-p <http port>]\n", prog);
	exit(EXIT_FAILURE);
}

int main(int argc, char *argv[])
{
	int i;

	ibcast->sin_family = AF_INET;
	ibcast->sin_addr.s_addr = INADDR_BROADCAST;
	ibcast->sin_port = htons(PROBE_PORT);

	ilocal->sin_family = AF_INET;
	ilocal->sin_addr.s_addr = INADDR_ANY;
	ilocal->sin_port = htons(HTTP_PORT);

	while ((i = getopt(argc, argv, "b:l:p:")) != EOF) {
		switch(i) {
		case 'b':
			if (!inet_aton(optarg, &ibcast->sin_addr)) {
				fprintf(stderr, "invalid broadcast address %s\n", optarg);
				exit(1);
			}
			break;
		case 'l':
			if (!inet_aton(optarg, &ilocal->sin_addr)) {
				fprintf(stderr, "invalid local address %s\n", optarg);
				exit(1);
			}
			break;
		case 'p':
			ilocal->sin_port = htons(atoi(optarg));
			break;
		default:
			usage(argv[0]);
		}
	}

	for (i=0; i<MAXFRAGS; i++)
		fragarray[i].iov_base = fragbuf+(FRAGSPACE*i);
	fragspace[0].iov_base = (void *)vidpart;
	fragspace[0].iov_len = sizeof(vidpart)-1;
	signal(SIGINT, sighandle);
	signal(SIGTERM, sighandle);
	startup();
}
