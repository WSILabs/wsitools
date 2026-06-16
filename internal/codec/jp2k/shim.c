// OpenJPEG (libopenjp2) encode shim: interleaved RGB888 tile -> raw J2K
// codestream (SOC FF4F …), suitable for TIFF JP2K tiles and DICOM frame data.
// Mirrors the structure of internal/codec/htj2k's OpenJPH shim.
#include <stdlib.h>
#include <string.h>
#include <openjpeg.h>

// Growable in-memory output buffer for the OpenJPEG stream callbacks.
typedef struct {
	unsigned char *data;
	size_t size; // logical end (highest byte written)
	size_t cap;  // allocated capacity
	size_t pos;  // current cursor
} membuf;

static int membuf_reserve(membuf *m, size_t need) {
	if (need <= m->cap) {
		return 1;
	}
	size_t newcap = m->cap ? m->cap : 65536;
	while (newcap < need) {
		newcap *= 2;
	}
	unsigned char *nd = (unsigned char *)realloc(m->data, newcap);
	if (!nd) {
		return 0;
	}
	// Zero new space so skipped-but-never-written regions are deterministic.
	memset(nd + m->cap, 0, newcap - m->cap);
	m->data = nd;
	m->cap = newcap;
	return 1;
}

static OPJ_SIZE_T mem_write(void *buffer, OPJ_SIZE_T n, void *user) {
	membuf *m = (membuf *)user;
	if (!membuf_reserve(m, m->pos + n)) {
		return (OPJ_SIZE_T)-1;
	}
	memcpy(m->data + m->pos, buffer, n);
	m->pos += n;
	if (m->pos > m->size) {
		m->size = m->pos;
	}
	return n;
}

// Skip on a write stream reserves space (later filled via seek+write).
static OPJ_OFF_T mem_skip(OPJ_OFF_T n, void *user) {
	membuf *m = (membuf *)user;
	if (n < 0) {
		return -1;
	}
	if (!membuf_reserve(m, m->pos + (size_t)n)) {
		return -1;
	}
	m->pos += (size_t)n;
	if (m->pos > m->size) {
		m->size = m->pos;
	}
	return n;
}

static OPJ_BOOL mem_seek(OPJ_OFF_T n, void *user) {
	membuf *m = (membuf *)user;
	if (n < 0) {
		return OPJ_FALSE;
	}
	if (!membuf_reserve(m, (size_t)n)) {
		return OPJ_FALSE;
	}
	m->pos = (size_t)n;
	if (m->pos > m->size) {
		m->size = m->pos;
	}
	return OPJ_TRUE;
}

static int clamp_resolutions(int w, int h) {
	int m = w < h ? w : h;
	int r = 1;
	while ((1 << r) <= m && r < 6) {
		r++;
	}
	return r; // 1..6
}

// wsi_jp2k_encode encodes a width*height interleaved RGB888 tile.
// reversible != 0  -> lossless 5/3 wavelet (quality ignored).
// reversible == 0  -> lossy 9/7 wavelet at a quality-derived compression ratio.
// On success returns 0 and sets *outbuf (caller frees) / *outsize.
int wsi_jp2k_encode(const unsigned char *rgb, int width, int height,
                    int quality, int reversible,
                    unsigned char **outbuf, size_t *outsize) {
	if (!rgb || width <= 0 || height <= 0) {
		return 1;
	}
	opj_cparameters_t params;
	opj_set_default_encoder_parameters(&params);
	params.tcp_numlayers = 1;
	params.cp_disto_alloc = 1;
	params.numresolution = clamp_resolutions(width, height);
	params.tcp_mct = 1; // RGB multi-component (color decorrelating) transform
	if (reversible) {
		params.irreversible = 0;     // reversible 5/3
		params.tcp_rates[0] = 0.0f;  // 0 => lossless (no rate cap)
	} else {
		params.irreversible = 1; // irreversible 9/7
		// quality 1..100 -> compression ratio; 100 -> ~lossless, lower -> higher ratio.
		float ratio = (float)(100 - quality);
		if (ratio < 1.0f) {
			ratio = 1.0f;
		}
		params.tcp_rates[0] = ratio;
	}

	opj_image_cmptparm_t cmpt[3];
	memset(cmpt, 0, sizeof(cmpt));
	for (int c = 0; c < 3; c++) {
		cmpt[c].prec = 8;
		cmpt[c].bpp = 8;
		cmpt[c].sgnd = 0;
		cmpt[c].dx = 1;
		cmpt[c].dy = 1;
		cmpt[c].w = (OPJ_UINT32)width;
		cmpt[c].h = (OPJ_UINT32)height;
	}
	opj_image_t *image = opj_image_create(3, cmpt, OPJ_CLRSPC_SRGB);
	if (!image) {
		return 2;
	}
	image->x0 = 0;
	image->y0 = 0;
	image->x1 = (OPJ_UINT32)width;
	image->y1 = (OPJ_UINT32)height;
	int npix = width * height;
	for (int i = 0; i < npix; i++) {
		image->comps[0].data[i] = rgb[i * 3 + 0];
		image->comps[1].data[i] = rgb[i * 3 + 1];
		image->comps[2].data[i] = rgb[i * 3 + 2];
	}

	opj_codec_t *codec = opj_create_compress(OPJ_CODEC_J2K);
	if (!codec) {
		opj_image_destroy(image);
		return 3;
	}
	if (!opj_setup_encoder(codec, &params, image)) {
		opj_destroy_codec(codec);
		opj_image_destroy(image);
		return 4;
	}

	membuf mb;
	memset(&mb, 0, sizeof(mb));
	opj_stream_t *stream = opj_stream_create(1 << 20, OPJ_FALSE /* output */);
	if (!stream) {
		opj_destroy_codec(codec);
		opj_image_destroy(image);
		return 5;
	}
	opj_stream_set_user_data(stream, &mb, NULL);
	opj_stream_set_write_function(stream, mem_write);
	opj_stream_set_skip_function(stream, mem_skip);
	opj_stream_set_seek_function(stream, mem_seek);

	OPJ_BOOL ok = opj_start_compress(codec, image, stream) &&
	              opj_encode(codec, stream) &&
	              opj_end_compress(codec, stream);

	opj_stream_destroy(stream);
	opj_destroy_codec(codec);
	opj_image_destroy(image);

	if (!ok) {
		free(mb.data);
		return 6;
	}
	*outbuf = mb.data; // caller frees with free()
	*outsize = mb.size;
	return 0;
}
