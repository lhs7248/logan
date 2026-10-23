package test.logan.dianping.com.logan;

import org.json.JSONObject;
import java.net.URI;

/** Private runtime configuration: never supplied through an Intent or compiled into the APK. */
final class WorkbenchProfile {
    final String receiverUrl, appId, uploadToken, deviceId, projectId;
    final byte[] key, iv;
    private WorkbenchProfile(JSONObject json) throws Exception {
        receiverUrl = required(json, "receiverUrl");
        URI uri = new URI(receiverUrl);
        if (!"http".equals(uri.getScheme()) || !"127.0.0.1".equals(uri.getHost()) || uri.getPort() != 8091
                || !"/logan-web/api/workbench/v1/sdk/native".equals(uri.getPath())
                || uri.getRawQuery() != null || uri.getRawFragment() != null || uri.getUserInfo() != null) {
            throw new IllegalArgumentException("Profile receiver must use the local USB receiver");
        }
        appId = required(json, "appId");
        if (!"test.logan.dianping.com.logan.workbench".equals(appId)) throw new IllegalArgumentException("Profile application identity differs");
        uploadToken = required(json, "uploadToken");
        if (uploadToken.length() < 16) throw new IllegalArgumentException("Profile upload credential is invalid");
        deviceId = required(json, "deviceId"); projectId = required(json, "projectId");
        key = decode16(required(json, "nativeKeyBase64")); iv = decode16(required(json, "nativeIVBase64"));
    }
    static WorkbenchProfile parse(JSONObject json) {
        try { return new WorkbenchProfile(json); }
        catch (IllegalArgumentException e) { throw e; }
        catch (Exception e) { throw new IllegalArgumentException("Private profile is malformed"); }
    }
    private static String required(JSONObject json, String name) throws Exception {
        String value = json.getString(name);
        if (value.trim().isEmpty() || value.length() > 512 || value.indexOf('\r') >= 0 || value.indexOf('\n') >= 0)
            throw new IllegalArgumentException("Private profile has an invalid field: " + name);
        return value;
    }
    private static byte[] decode16(String text) {
        if (!text.matches("[A-Za-z0-9+/]{22}==")) throw new IllegalArgumentException("Native key and IV must each decode to 16 bytes");
        String alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
        byte[] decoded = new byte[16]; int buffer=0,bits=0,index=0;
        for (int i=0;i<22;i++) {buffer=(buffer<<6)|alphabet.indexOf(text.charAt(i));bits+=6;if(bits>=8){bits-=8;decoded[index++]=(byte)(buffer>>bits);}}
        if ((buffer & 15) != 0) throw new IllegalArgumentException("Native key encoding is invalid");
        for(byte value:decoded) if(value<33 || value>126) throw new IllegalArgumentException("Legacy native SDK requires 16 printable ASCII key/IV bytes");
        return decoded;
    }
}
