
const SUPABASE_URL = "YOUR_SUPABASE_URL";
const SUPABASE_KEY = "YOUR_SUPABASE_KEY";


// This is the secret key used to sign the math. Keep it identical in Go and here.
const HMAC_SECRET = "YOUR_SECRET_HMAC_KEY";  

const urlCache = new Map();
const CACHE_TTL = 60 * 1000;

// Web Crypto API HMAC Verification
async function verifySignature(machineId, timestamp, signatureHex) {
    const timeDiff = Math.abs(Date.now() - parseInt(timestamp));
    if (timeDiff > 60000) return false; // Reject requests older than 60 seconds (Replay Attack Protection)

    const encoder = new TextEncoder();
    const key = await crypto.subtle.importKey(
        "raw", encoder.encode(HMAC_SECRET),
        { name: "HMAC", hash: "SHA-256" },
        false, ["verify"]
    );

    const data = encoder.encode(machineId + timestamp);
    // Convert hex string to Uint8Array
    const sigBuf = new Uint8Array(signatureHex.match(/[\da-f]{2}/gi).map(h => parseInt(h, 16)));
    
    return await crypto.subtle.verify("HMAC", key, sigBuf, data);
}

export default {
  async fetch(request, env, ctx) {
    const corsHeaders = {
      "Access-Control-Allow-Origin": "*",
      "Access-Control-Allow-Methods": "GET, POST, OPTIONS",
      "Access-Control-Allow-Headers": "Content-Type, X-Timestamp, X-Signature",
    };

    if (request.method === "OPTIONS") return new Response(null, { headers: corsHeaders });

    const supabaseUrl = env.SUPABASE_URL || SUPABASE_URL;
    const supabaseKey = env.SUPABASE_KEY || SUPABASE_KEY;

    const url = new URL(request.url);
    const pathParts = url.pathname.split('/').filter(p => p !== '');

    if (pathParts.length === 0) return new Response("🛡️ BareVault Gateway Active", { status: 200, headers: corsHeaders });

    // --- SECURE API: SYNC URL ---
    if (pathParts[0] === "api" && pathParts[1] === "sync") {
      if (request.method !== "POST") return new Response("Method not allowed", { status: 405, headers: corsHeaders });
      
      const body = await request.json();
      
      // 🛡️ THE CRYPTO CHECK: Verify the timestamp and signature
      const reqTimestamp = request.headers.get("X-Timestamp");
      const reqSignature = request.headers.get("X-Signature");
      
      if (!reqTimestamp || !reqSignature || !(await verifySignature(body.p_machine_name, reqTimestamp, reqSignature))) {
          return new Response("Unauthorized or Expired Request", { status: 403, headers: corsHeaders });
      }

      const res = await fetch(`${supabaseUrl}/rest/v1/rpc/sync_vault_url`, {
        method: 'POST',
        headers: {
          'apikey': supabaseKey,
          'Authorization': `Bearer ${supabaseKey}`,
          'Content-Type': 'application/json'
        },
        body: JSON.stringify(body)
      });
      
      const data = await res.json();
      return new Response(JSON.stringify(data), { headers: { "Content-Type": "application/json", ...corsHeaders } });
    }

    // --- NORMAL ROUTING ---
    const machineId = pathParts[0];
    const remainingPath = pathParts.slice(1).join('/');
    const searchParams = url.searchParams;
    const isResolveOnly = searchParams.get("resolve") === "true"; 
    searchParams.delete("resolve");

    const isApi = machineId.endsWith('-api'); 
    const isRootRequest = remainingPath === ''; 
    const shouldBypassCache = isApi || isRootRequest;

    let tunnelUrl = null;
    const cached = urlCache.get(machineId);

    if (!shouldBypassCache && cached && (Date.now() - cached.timestamp < CACHE_TTL)) {
      tunnelUrl = cached.url; 
    } else {
      const response = await fetch(`${supabaseUrl}/rest/v1/rpc/get_vault_url`, {
        method: 'POST',
        headers: {
          'apikey': supabaseKey,
          'Authorization': `Bearer ${supabaseKey}`,
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({ p_machine_name: machineId })
      });

      tunnelUrl = await response.json();

      if (!tunnelUrl) {
        return new Response(`❌ Vault '${machineId}' not found or offline.`, { 
          status: 404, headers: { "Content-Type": "text/plain", ...corsHeaders }
        });
      }
      urlCache.set(machineId, { url: tunnelUrl, timestamp: Date.now() });
    }

    let finalUrl = tunnelUrl;
    if (remainingPath) finalUrl += `/${remainingPath}`;
    const searchString = searchParams.toString();
    if (searchString) finalUrl += `?${searchString}`;

    if (isResolveOnly) return new Response(finalUrl, { status: 200, headers: { "Content-Type": "text/plain", ...corsHeaders } });

    return new Response(null, { status: 302, headers: { "Location": finalUrl, ...corsHeaders } });
  }
};
