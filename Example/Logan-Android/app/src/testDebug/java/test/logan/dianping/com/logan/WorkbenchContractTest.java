package test.logan.dianping.com.logan;
import org.json.JSONObject;
public final class WorkbenchContractTest {
 @org.junit.Test public void contract() throws Exception { main(new String[0]); }
 public static void main(String[] args) throws Exception {
  WorkbenchProfile p = WorkbenchProfile.parse(profile("http://127.0.0.1:8091/logan-web/api/workbench/v1/sdk/native", "test.logan.dianping.com.logan.workbench"));
  check(p.deviceId.equals("test-device"), "profile device");
  reject(profile("http://example.com/upload", p.appId));
  reject(profile("http://127.0.0.1:8091/logan-web/api/workbench/v1/sdk/native?token=secret", p.appId));
  reject(profile(p.receiverUrl, "legacy.package"));
  JSONObject invalid = profile(p.receiverUrl, p.appId); invalid.put("nativeKeyBase64", "bad"); reject(invalid);
  JSONObject binary = profile(p.receiverUrl,p.appId); binary.put("nativeKeyBase64", "/////////////////////w=="); reject(binary);
  check(WorkbenchReceipt.describe(200, "{}").startsWith("Upload failed"), "2xx without receipt cannot mean accepted");
  check(WorkbenchReceipt.describe(401, "{}").contains("credential"), "auth failure");
  String accepted="{\"code\":200,\"data\":{\"receiptId\":\"r1\",\"assetId\":\"a1\",\"runId\":\"p1\",\"status\":\"running\"}}";
  String text=WorkbenchReceipt.describe(200,accepted);
  check(text.contains("Accepted") && text.contains("not confirmed") && text.contains("a1"), "accepted is not parsed");
  check(WorkbenchReceipt.describe(200,accepted.replace("running", "succeeded")).contains("Parsed"), "completed run");
  System.out.println("Workbench contract tests passed");
 }
 static JSONObject profile(String url,String app) throws Exception {return new JSONObject().put("receiverUrl",url).put("appId",app).put("uploadToken","test-only-credential-value").put("nativeKeyBase64","MDEyMzQ1Njc4OTAxMjM0NQ==").put("nativeIVBase64","MDEyMzQ1Njc4OTAxMjM0NQ==").put("deviceId","test-device").put("projectId","local-mobile");}
 static void reject(JSONObject j)throws Exception {try {WorkbenchProfile.parse(j);throw new AssertionError("invalid profile accepted");}catch(IllegalArgumentException expected){}}
 static void check(boolean ok,String message){if(!ok)throw new AssertionError(message);}
}
