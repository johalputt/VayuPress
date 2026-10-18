// SPDX-License-Identifier: Apache-2.0

package main

// handlers_member_account_script.go — the account page's script, emitted with
// the request's nonce. It talks only to same-origin member endpoints, and puts
// response data in with textContent and img.src, never innerHTML.
//
// The page stands without it: rows that open in place are drawn open, and the
// name and email choices post as one form. With it, those rows fold behind
// their button, a switch saves the moment it is flipped, a picture is chosen
// with one tap, and two-factor runs in place.

// memberAccountInlineJS returns the page's <script nonce=…> block. The JS
// contains no backticks so it can live inside a Go raw string.
func memberAccountInlineJS(nonce string) string {
	return `<script nonce="` + nonce + `">
(function(){'use strict';
function q(s,r){return (r||document).querySelector(s);}
function qa(s,r){return Array.prototype.slice.call((r||document).querySelectorAll(s));}
function postJSON(url,body){return fetch(url,{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json'},body:JSON.stringify(body||{})}).then(function(res){return res.json().then(function(j){return {ok:res.ok,j:j};},function(){return {ok:res.ok,j:{}};});});}
function say(el,text,err){if(!el)return;el.textContent=text||'';el.classList.toggle('is-err',!!err);}
function errMsg(r,fallback){return (r&&r.j&&r.j.error&&r.j.error.message)||fallback;}

/* ---- Rows that open in place ---- */
function setOpen(btn,open){
  var more=document.getElementById(btn.getAttribute('data-acct-open'));
  if(!more)return;
  more.classList.toggle('is-open',open);
  qa('[data-acct-open="'+more.id+'"]').forEach(function(b){if(b.hasAttribute('aria-expanded'))b.setAttribute('aria-expanded',open?'true':'false');});
  if(open){var f=q('input:not([type=hidden]):not([type=file]),button',more);if(f&&f.focus)f.focus({preventScroll:true});}
}
qa('[data-acct-open]').forEach(function(btn){
  btn.addEventListener('click',function(e){
    e.preventDefault();
    var more=document.getElementById(btn.getAttribute('data-acct-open'));
    var open=!(more&&more.classList.contains('is-open'));
    setOpen(btn,open);
    if(open&&btn.tagName==='A'&&more){more.parentNode.scrollIntoView({behavior:'smooth',block:'center'});}
  });
});
/* A notice about a row, or a link to it, opens that row. */
var hash=window.location.hash.replace('#','');
if(hash){var hb=q('[data-acct-open="'+hash+'-more"]');if(hb)setOpen(hb,true);}

/* ---- Email switches save as they change ---- */
var form=document.getElementById('acct-form');
qa('[data-acct-switch]').forEach(function(sw){
  sw.addEventListener('change',function(){
    var note=q('[data-acct-note]',sw.closest('.acct-sec'));
    var body=new URLSearchParams();
    qa('[form="acct-form"]').forEach(function(el){
      if(!el.name)return;
      if(el.type==='checkbox'){if(el.checked)body.append(el.name,el.value);}else{body.append(el.name,el.value);}
    });
    sw.disabled=true;
    fetch(form.action,{method:'POST',credentials:'same-origin',headers:{'Accept':'application/json','Content-Type':'application/x-www-form-urlencoded'},body:body.toString()})
      .then(function(res){if(!res.ok)throw new Error();say(note,'Saved');})
      .catch(function(){sw.checked=!sw.checked;say(note,'Not saved. Try again.',true);})
      .then(function(){sw.disabled=false;});
  });
});

/* ---- Picture ---- */
var pickMsg=q('[data-acct-pick-msg]');
function pictureSaved(r){
  if(!r.ok){say(pickMsg,errMsg(r,'Your picture was not saved. Try again.'),true);return;}
  window.location.assign('/members/account?saved=picture#profile');
}
qa('[data-acct-avatar]').forEach(function(b){
  b.addEventListener('click',function(){
    say(pickMsg,'Saving…');
    postJSON('/api/v1/members/avatar/choose',{choice:b.getAttribute('data-acct-avatar')}).then(pictureSaved);
  });
});
var up=q('[data-acct-upload]');
if(up){up.addEventListener('change',function(){
  var file=up.files&&up.files[0];
  if(!file)return;
  if(file.size>100*1024){say(pickMsg,'That photo is over 100 KB. Choose a smaller one.',true);up.value='';return;}
  var fd=new FormData();fd.append('avatar',file);
  say(pickMsg,'Uploading…');
  fetch('/api/v1/members/avatar',{method:'POST',credentials:'same-origin',body:fd})
    .then(function(res){return res.json().then(function(j){return {ok:res.ok,j:j};},function(){return {ok:res.ok,j:{}};});})
    .then(pictureSaved);
});}

/* ---- Advertising and claiming an address live in the account panel ---- */
qa('[data-acct-portal]').forEach(function(b){
  b.addEventListener('click',function(){if(window.vpPortalOpen)window.vpPortalOpen(b.getAttribute('data-acct-portal'));});
});

/* ---- Two-factor ---- */
var card=q('[data-totp-card]');
if(card){
  var beginBtn=q('[data-totp-begin]',card),enroll=q('[data-totp-enroll]',card),qrEl=q('[data-totp-qr]',card),keyEl=q('[data-totp-key]',card);
  var uriEl=q('[data-totp-uri]',card),codeEl=q('[data-totp-code]',card),verifyBtn=q('[data-totp-verify]',card),disableBtn=q('[data-totp-disable]',card),mEl=q('[data-totp-msg]',card);
  if(beginBtn){beginBtn.addEventListener('click',function(){
    beginBtn.disabled=true;
    postJSON('/api/v1/members/totp/begin').then(function(r){
      if(!r.ok||!r.j.secret){say(mEl,errMsg(r,'Two-factor could not be started'),true);beginBtn.disabled=false;return;}
      if(keyEl)keyEl.textContent=r.j.secret;
      if(uriEl&&r.j.uri)uriEl.setAttribute('href',r.j.uri);
      if(qrEl&&r.j.qr)qrEl.src=r.j.qr;
      if(enroll)enroll.hidden=false;
      beginBtn.hidden=true;
      if(codeEl)codeEl.focus();
    });
  });}
  function code(){return codeEl?codeEl.value.replace(/\D/g,''):'';}
  if(verifyBtn){verifyBtn.addEventListener('click',function(){
    if(code().length!==6){say(mEl,'Enter the 6-digit code from your app',true);return;}
    verifyBtn.disabled=true;
    postJSON('/api/v1/members/totp/verify',{code:code()}).then(function(r){
      if(!r.ok){say(mEl,errMsg(r,'That code is not right'),true);verifyBtn.disabled=false;return;}
      window.location.assign('/members/account?twofa=on#security');
    });
  });}
  if(disableBtn){disableBtn.addEventListener('click',function(){
    if(code().length!==6){say(mEl,'Enter a current 6-digit code from your app',true);return;}
    disableBtn.disabled=true;
    postJSON('/api/v1/members/totp/disable',{code:code()}).then(function(r){
      if(!r.ok){say(mEl,errMsg(r,'Two-factor could not be turned off'),true);disableBtn.disabled=false;return;}
      window.location.assign('/members/account?twofa=off#security');
    });
  });}
}

})();
</script>`
}
