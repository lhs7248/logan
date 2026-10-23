package test.logan.dianping.com.logan;

import android.app.Activity;
import android.content.Intent;
import android.content.pm.PackageInfo;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.os.SystemClock;
import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import com.dianping.logan.Logan;
import org.json.JSONArray;
import org.json.JSONObject;
import java.io.File;
import java.io.FileInputStream;
import java.io.ByteArrayOutputStream;
import java.nio.charset.StandardCharsets;
import java.text.SimpleDateFormat;
import java.util.Date;
import java.util.HashMap;
import java.util.Locale;
import java.util.Map;
import java.util.UUID;

public final class WorkbenchActivity extends Activity {
    private TextView state, capture;
    private Button collect, upload;
    private WorkbenchApplication app;
    private String marker, fileDate;
    private final Handler handler=new Handler(Looper.getMainLooper());
    private boolean destroyed;
    @Override public void onCreate(Bundle saved) {
        super.onCreate(saved);
        app=(WorkbenchApplication)getApplication();
        app.submissions.invalidate();
        ScrollView scroll=new ScrollView(this);LinearLayout panel=new LinearLayout(this);panel.setOrientation(LinearLayout.VERTICAL);panel.setPadding(32,32,32,32);scroll.addView(panel);setContentView(scroll);
        TextView title=new TextView(this);title.setText("Logan · Android Workbench");title.setTextSize(24);panel.addView(title);
        TextView info=new TextView(this);info.setText(BuildConfig.APPLICATION_ID+"\nReal Java / JNI / CLogan · private storage");panel.addView(info);
        collect=button(panel,R.id.workbench_collect,"Create 3 marker logs");upload=button(panel,R.id.workbench_upload,"Upload current log");
        Button legacy=button(panel,R.id.workbench_legacy,"Open legacy example");
        state=new TextView(this);state.setId(R.id.workbench_status);state.setTextIsSelectable(true);panel.addView(state);
        capture=new TextView(this);capture.setId(R.id.workbench_capture);capture.setTextIsSelectable(true);panel.addView(capture);
        collect.setEnabled(app.profile!=null);upload.setEnabled(false);
        state.setText(app.profile==null?app.initializationError:"Private profile loaded. Ready to collect actual native logs.");
        try {JSONObject previous=read("workbench-last-capture.json");marker=previous.getString("marker");fileDate=previous.getString("fileDate");capture.setText(previous.getJSONArray("messages").join("\n"));upload.setEnabled(app.profile!=null);state.append("\nLast capture restored.");}catch(Exception ignored){}
        try {state.append("\nLast upload: "+read("workbench-last-upload.json").getString("display"));}catch(Exception ignored){}
        collect.setOnClickListener(v->collect());upload.setOnClickListener(v->upload());
        legacy.setOnClickListener(v->startActivity(new Intent(this,MainActivity.class)));
    }
    private Button button(LinearLayout parent,int id,String label){Button b=new Button(this);b.setId(id);b.setText(label);parent.addView(b);return b;}
    private void collect() {
        app.submissions.invalidate();
        try {
            String installation=getPreferences(MODE_PRIVATE).getString("installation",null);
            if(installation==null){installation=UUID.randomUUID().toString();getPreferences(MODE_PRIVATE).edit().putString("installation",installation).commit();}
            marker="android-workbench-"+UUID.randomUUID();fileDate=new SimpleDateFormat("yyyy-MM-dd",Locale.US).format(new Date());
            JSONArray messages=new JSONArray();for(int i=1;i<=3;i++){String message=marker+" record="+i+" installation="+installation;messages.put(message);Logan.w(message,i);}
            Logan.f();
            JSONObject record=new JSONObject().put("marker",marker).put("installation",installation).put("fileDate",fileDate).put("messages",messages);
            save("workbench-last-capture.json",record);capture.setText(messages.join("\n"));state.setText("3 unique marker records queued and flush requested. Ready to upload.");upload.setEnabled(true);
        }catch(Exception e){state.setText("Capture failed: private state could not be saved or native SDK unavailable.");}
    }
    private void upload() {
        final WorkbenchSubmission.Attempt attempt=app.submissions.begin(marker,fileDate,SystemClock.elapsedRealtime());
        try {
            PackageInfo pkg=getPackageManager().getPackageInfo(getPackageName(),0);
            Map<String,String> headers=new HashMap<>();headers.put("X-Logan-Upload-Token",app.profile.uploadToken);headers.put("X-Project-ID",app.profile.projectId);
            headers.put("appId",BuildConfig.APPLICATION_ID);headers.put("deviceId",app.profile.deviceId);headers.put("fileDate",attempt.fileDate);headers.put("platform","1");
            headers.put("buildVersion",String.valueOf(pkg.versionCode));headers.put("appVersion",pkg.versionName);
            upload.setEnabled(false);collect.setEnabled(false);state.setText("Uploading encrypted native log…\nSubmission: "+attempt.id+"\nCapture: "+attempt.marker);
            persistResult(attempt,"pending",0,"Awaiting SDK callback",null);
            handler.postDelayed(()->{
                if(destroyed || !app.submissions.timeout(attempt,SystemClock.elapsedRealtime()))return;
                String message="Upload callback timed out after 30 seconds. The SDK transport was not cancelled; the server may have received this submission. Check the workbench before retrying. The native SDK may also have no readable dated log file.\nSubmission: "+attempt.id+"\nCapture: "+attempt.marker;
                persistResult(attempt,"timeout",0,message,null);
                state.setText(message);restoreControls();
            },WorkbenchSubmission.TIMEOUT_MILLIS);
            Logan.f();
            Logan.s(app.profile.receiverUrl,attempt.fileDate,headers,(status,bytes)->{
                final String body=bytes==null?"":new String(bytes,StandardCharsets.UTF_8);
                handler.post(()->{
                    if(destroyed || !app.submissions.complete(attempt))return;
                    String description=WorkbenchReceipt.describe(status,body)+"\nSubmission: "+attempt.id+"\nCapture: "+attempt.marker;
                    persistResult(attempt,"callback",status,description,body);
                    state.setText(description);restoreControls();
                });
            });
        }catch(Exception e){
            if(app.submissions.complete(attempt)){
                String message="Upload could not be submitted: installed package metadata or native SDK unavailable.\nSubmission: "+attempt.id+"\nCapture: "+attempt.marker;
                persistResult(attempt,"submission_error",0,message,null);state.setText(message);restoreControls();
            }
        }
    }
    private void restoreControls(){upload.setEnabled(app.profile!=null && marker!=null);collect.setEnabled(app.profile!=null);}
    private void persistResult(WorkbenchSubmission.Attempt attempt,String outcome,int status,String description,String body){
        try {
            JSONObject result=new JSONObject().put("submissionId",attempt.id).put("marker",attempt.marker).put("fileDate",attempt.fileDate).put("outcome",outcome).put("display",description);
            if("callback".equals(outcome))result.put("httpStatus",status);
            if(status>=200 && status<300 && body!=null){
                JSONObject data=new JSONObject(body).optJSONObject("data");
                if(data!=null)for(String key:new String[]{"receiptId","assetId","runId","status"}){
                    String value=data.optString(key,"");if(value.matches("[A-Za-z0-9._:-]{1,128}"))result.put(key,value);
                }
            }
            save("workbench-last-upload.json",result);
        }catch(Exception ignored){}
    }
    @Override protected void onDestroy(){destroyed=true;handler.removeCallbacksAndMessages(null);super.onDestroy();}
    private void save(String name,JSONObject value)throws Exception{try(java.io.FileOutputStream out=openFileOutput(name,MODE_PRIVATE)){out.write(value.toString().getBytes(StandardCharsets.UTF_8));out.getFD().sync();}}
    private JSONObject read(String name)throws Exception {ByteArrayOutputStream out=new ByteArrayOutputStream();try(FileInputStream in=new FileInputStream(new File(getFilesDir(),name))){byte[] b=new byte[1024];int n;while((n=in.read(b))!=-1){out.write(b,0,n);if(out.size()>32768)throw new IllegalArgumentException();}}return new JSONObject(new String(out.toByteArray(),StandardCharsets.UTF_8));}
}
