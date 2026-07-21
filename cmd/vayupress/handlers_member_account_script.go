package main

// handlers_member_account_script.go — the small, CSP-safe inline script that powers
// the member dashboard's interactive cards: self-serve 2FA enrolment (scannable
// QR + verify + code-gated disable) and included-mailbox claim. It is emitted
// with the request nonce and talks only to same-origin member endpoints. No
// eval, no innerHTML with response data — secrets/keys go in via textContent and
// the QR via img.src, matching the strict-CSP admin scripts.

// memberAccountInlineJS returns a <script nonce=…> block for the member account
// page. The JS contains no backticks so it can live inside a Go raw string.
func memberAccountInlineJS(nonce string) string {
	return `<script nonce="` + nonce + `">
(function(){'use strict';
function q(s,r){return (r||document).querySelector(s);}
function postJSON(url,body){return fetch(url,{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json'},body:JSON.stringify(body||{})}).then(function(res){return res.json().then(function(j){return {ok:res.ok,j:j};},function(){return {ok:res.ok,j:{}};});});}
function setMsg(el,text,err){if(!el)return;el.textContent=text||'';el.className='ma-claim-msg'+(err?' is-err':(text?' is-ok':''));}
function errMsg(r,fallback){return (r&&r.j&&r.j.error&&r.j.error.message)||fallback;}

/* ---- Two-factor enrolment (mirrors the operator flow, member-scoped) ---- */
var card=q('[data-totp-card]');
if(card){
  var beginBtn=q('[data-totp-begin]',card);
  var enroll=q('[data-totp-enroll]',card);
  var qrEl=q('[data-totp-qr]',card);
  var keyEl=q('[data-totp-key]',card);
  var uriEl=q('[data-totp-uri]',card);
  var codeEl=q('[data-totp-code]',card);
  var verifyBtn=q('[data-totp-verify]',card);
  var disableBtn=q('[data-totp-disable]',card);
  var mEl=q('[data-totp-msg]',card);
  if(beginBtn){beginBtn.addEventListener('click',function(){
    beginBtn.disabled=true;
    postJSON('/api/v1/members/totp/begin').then(function(r){
      if(!r.ok||!r.j.secret){setMsg(mEl,errMsg(r,'Could not start 2FA setup'),true);beginBtn.disabled=false;return;}
      if(keyEl)keyEl.textContent=r.j.secret;
      if(uriEl&&r.j.uri)uriEl.setAttribute('href',r.j.uri);
      if(qrEl&&r.j.qr)qrEl.src=r.j.qr;
      if(enroll)enroll.hidden=false;
      if(codeEl)codeEl.focus();
    });
  });}
  if(verifyBtn){verifyBtn.addEventListener('click',function(){
    var code=codeEl?codeEl.value.replace(/\D/g,''):'';
    if(code.length!==6){setMsg(mEl,'Enter the 6-digit code from your app',true);return;}
    verifyBtn.disabled=true;
    postJSON('/api/v1/members/totp/verify',{code:code}).then(function(r){
      if(!r.ok){setMsg(mEl,errMsg(r,'That code is not valid'),true);verifyBtn.disabled=false;return;}
      window.location.assign('/members/account?twofa=on');
    });
  });}
  if(disableBtn){disableBtn.addEventListener('click',function(){
    var code=codeEl?codeEl.value.replace(/\D/g,''):'';
    if(code.length!==6){window.alert('Enter your current 6-digit code to turn 2FA off.');return;}
    disableBtn.disabled=true;
    postJSON('/api/v1/members/totp/disable',{code:code}).then(function(r){
      if(!r.ok){window.alert(errMsg(r,'Could not disable 2FA'));disableBtn.disabled=false;return;}
      window.location.assign('/members/account?twofa=off');
    });
  });}
}

/* ---- Included-mailbox claim ---- */
var claim=q('[data-claim-domain]');
if(claim){
  var localEl=q('#ma-claim-local');
  var passEl=q('#ma-claim-pass');
  var termsEl=q('#ma-claim-terms');
  var btn=q('#ma-claim-btn');
  var cmsg=q('#ma-claim-msg');
  if(btn){btn.addEventListener('click',function(){
    var local=(localEl&&localEl.value||'').trim().toLowerCase();
    var pass=(passEl&&passEl.value||'');
    if(!local){setMsg(cmsg,'Choose an address.',true);return;}
    if(pass.length<8){setMsg(cmsg,'Password must be at least 8 characters.',true);return;}
    if(termsEl&&!termsEl.checked){setMsg(cmsg,'Please accept the mailbox terms.',true);return;}
    btn.disabled=true;setMsg(cmsg,'Creating your mailbox…',false);
    postJSON('/api/v1/members/mailbox/claim',{localpart:local,password:pass,accept_terms:!!(termsEl&&termsEl.checked)}).then(function(r){
      if(!r.ok||!r.j.address){setMsg(cmsg,errMsg(r,'Could not create that address'),true);btn.disabled=false;return;}
      window.location.assign('/members/account?mail=claimed');
    });
  });}
}
})();
</script>`
}
