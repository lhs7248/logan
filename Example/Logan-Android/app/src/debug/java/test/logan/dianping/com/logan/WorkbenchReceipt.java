package test.logan.dianping.com.logan;
import org.json.JSONObject;
final class WorkbenchReceipt {
    static String describe(int httpStatus, String body) {
        if (httpStatus < 200 || httpStatus >= 300) {
            switch (httpStatus) {
                case -1: return "Upload failed: connection unavailable. Check local service and USB reverse.";
                case 401: return "Upload failed: application upload credential rejected. Re-provision private profile.";
                case 403: return "Upload failed: application, project or platform scope rejected.";
                case 409: return "Upload failed: upload identity conflicts with previous bytes.";
                case 413: return "Upload failed: encrypted log exceeds receiver size limit.";
                case 422: return "Upload failed: receiver rejected log metadata or content.";
                case 503: return "Upload failed: receiver storage or native decoder unavailable.";
                default: return "Upload failed: receiver HTTP " + httpStatus + ".";
            }
        }
        try {
            JSONObject envelope=new JSONObject(body), data=envelope.getJSONObject("data");
            String receipt=data.getString("receiptId"), asset=data.getString("assetId"), run=data.getString("runId");
            if(envelope.getInt("code")!=200 || !safe(receipt) || !safe(asset) || !safe(run)) throw new Exception();
            String status=data.optString("status", "unknown");
            String state="Accepted; parsing not confirmed";
            if("succeeded".equals(status) || "completed".equals(status)) state="Parsed successfully";
            else if("failed".equals(status)) state="Accepted; parsing failed. Inspect the run in the workbench";
            return state+"\nReceipt: "+receipt+"\nAsset: "+asset+"\nRun: "+run;
        } catch (Exception e) {return "Upload failed: receiver returned no valid acceptance receipt.";}
    }
    private static boolean safe(String id){return id.matches("[A-Za-z0-9._:-]{1,128}");}
}
