// SPDX-License-Identifier: Apache-2.0

package sitedoc

import (
	"crypto/sha256"
	"encoding/hex"
)

// GalleryJS turns a gallery's picture links into a viewer on the page: a
// <dialog> showing the picture with its description, closed by Escape, a
// click or the close button, and stepped with the arrow keys. Without it the
// links still open each picture full size, so nothing depends on it.
//
// Built with createElement/textContent — no markup is parsed — and served
// same-origin, so it runs under the strict CSP with no exception.
const GalleryJS = `(function(){` +
	`var links=[].slice.call(document.querySelectorAll('.vb-gallery a.vb-zoom'));` +
	`if(!links.length||typeof HTMLDialogElement!=='function')return;` +
	`var d=document.createElement('dialog');d.className='vb-lightbox';` +
	`var img=document.createElement('img');var cap=document.createElement('p');cap.className='vb-lightbox-cap';` +
	`var close=document.createElement('button');close.type='button';close.className='vb-lightbox-close';close.textContent='Close';close.setAttribute('aria-label','Close');` +
	`d.appendChild(close);d.appendChild(img);d.appendChild(cap);document.body.appendChild(d);` +
	`var at=0;function show(i){at=(i+links.length)%links.length;var a=links[at],p=a.querySelector('img');` +
	`img.src=a.getAttribute('href');img.alt=p?p.alt:'';cap.textContent=p?p.alt:'';` +
	`if(!d.open)d.showModal();}` +
	`links.forEach(function(a,i){a.addEventListener('click',function(e){e.preventDefault();show(i);});});` +
	`close.addEventListener('click',function(){d.close();});` +
	`d.addEventListener('click',function(e){if(e.target===d)d.close();});` +
	`d.addEventListener('keydown',function(e){if(e.key==='ArrowRight'){show(at+1);}else if(e.key==='ArrowLeft'){show(at-1);}});` +
	`})();`

// GalleryJSPath is where GalleryJS is served.
const GalleryJSPath = "/static/js/site-gallery.js"

var galleryJSHash = func() string {
	sum := sha256.Sum256([]byte(GalleryJS))
	return hex.EncodeToString(sum[:8])
}()

// GalleryScriptTag is the element that loads GalleryJS, versioned by its
// content so a change reaches browsers that cached the last one.
func GalleryScriptTag() string {
	return `<script src="` + GalleryJSPath + `?v=` + galleryJSHash + `" defer></script>`
}
