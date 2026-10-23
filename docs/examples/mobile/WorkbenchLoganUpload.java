import com.dianping.logan.Logan;
import com.dianping.logan.SendLogCallback;
import java.util.HashMap;
import java.util.Map;

/** Application-side adapter. Supply all configuration at runtime; never log headers. */
public final class WorkbenchLoganUpload {
    private WorkbenchLoganUpload() {}

    public static void upload(String endpoint, String date, String appId, String deviceId,
            String appVersion, String buildVersion, String uploadToken, String uploadId,
            SendLogCallback callback) {
        Map<String, String> headers = new HashMap<>();
        headers.put("appId", required(appId));
        headers.put("deviceId", required(deviceId));
        headers.put("platform", "1");
        headers.put("fileDate", required(date));
        optional(headers, "appVersion", appVersion);
        optional(headers, "buildVersion", buildVersion);
        headers.put("Content-Type", "application/octet-stream");
        headers.put("X-Logan-Upload-Token", required(uploadToken));
        if (uploadId != null) headers.put("X-Logan-Upload-ID", required(uploadId));
        Logan.s(required(endpoint), date, headers, callback);
    }

    private static void optional(Map<String, String> headers, String name, String value) {
        if (value != null && !value.isEmpty()) headers.put(name, required(value));
    }

    private static String required(String value) {
        if (value == null || value.length() == 0 || value.indexOf('\r') >= 0 || value.indexOf('\n') >= 0)
            throw new IllegalArgumentException("Missing or invalid upload configuration");
        return value;
    }
}
